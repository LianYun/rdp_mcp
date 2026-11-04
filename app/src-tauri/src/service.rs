//! 服务发现与进程管理（docs/ui.md §3）。
//!
//! Rust 侧保持精简：读取 `<data_dir>/run/service.json`、健康检查、以 sidecar 方式启动
//! 脱离 UI 的 `rdp-mcp serve`、通过管理 API 优雅停止（SIGTERM/SIGKILL 兜底）、
//! 以及 launchd/systemd 适配。

use serde::{Deserialize, Serialize};
use std::fs;
use std::io::{Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

const LAUNCHD_LABEL: &str = "com.lianyun.rdp-mcp";
const SYSTEMD_UNIT: &str = "rdp-mcp.service";

/// 用户主动停止/重启期间置位，避免监控线程把它当作异常退出。
pub static INTENTIONAL_STOP: AtomicBool = AtomicBool::new(false);

#[derive(Deserialize, Clone, Debug)]
struct ServiceJson {
    pid: i32,
    listen: String,
    admin_token: String,
    #[serde(default)]
    version: String,
    #[serde(default)]
    config_path: String,
    #[serde(default)]
    managed_by: String,
}

#[derive(Serialize, Clone, Debug, Default)]
pub struct Discovery {
    pub running: bool,
    pub listen: Option<String>,
    pub admin_token: Option<String>,
    pub managed_by: Option<String>,
    pub pid: Option<i32>,
    pub version: Option<String>,
    pub config_path: Option<String>,
    pub data_dir: String,
    /// 进程在但健康检查失败时的说明。
    pub error: Option<String>,
    /// 停止状态下检测到的、可用于启动的外部管理器（launchd/systemd）。
    pub manager: Option<String>,
}

/// 数据目录：`RDP_MCP_DATA_DIR`（测试/多实例用）优先，否则 `~/.rdp_mcp`。
pub fn data_dir() -> PathBuf {
    if let Ok(d) = std::env::var("RDP_MCP_DATA_DIR") {
        if !d.is_empty() {
            return PathBuf::from(d);
        }
    }
    home_dir().join(".rdp_mcp")
}

/// 是否使用了数据目录覆盖；此时不触碰 launchd/systemd，也不启用单实例插件。
pub fn data_dir_overridden() -> bool {
    std::env::var("RDP_MCP_DATA_DIR").map(|d| !d.is_empty()).unwrap_or(false)
}

fn home_dir() -> PathBuf {
    std::env::var("HOME").map(PathBuf::from).unwrap_or_else(|_| PathBuf::from("/"))
}

fn read_service_json() -> Option<ServiceJson> {
    let p = data_dir().join("run").join("service.json");
    let b = fs::read(p).ok()?;
    serde_json::from_slice(&b).ok()
}

fn pid_alive(pid: i32) -> bool {
    if pid <= 0 {
        return false;
    }
    // SAFETY: kill(pid, 0) 只做存在性检查。
    let r = unsafe { libc::kill(pid, 0) };
    r == 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
}

// ---------------------------------------------------------------- HTTP

pub struct HttpResponse {
    pub status: u16,
    pub body: String,
}

/// 最小 HTTP/1.1 客户端（仅用于访问本机管理 API，避免引入额外依赖）。
pub fn http(listen: &str, method: &str, path: &str, token: Option<&str>, timeout: Duration) -> Result<HttpResponse, String> {
    let addr = listen
        .to_socket_addrs()
        .map_err(|e| e.to_string())?
        .next()
        .ok_or_else(|| "bad listen address".to_string())?;
    let mut s = TcpStream::connect_timeout(&addr, timeout).map_err(|e| e.to_string())?;
    s.set_read_timeout(Some(timeout)).ok();
    s.set_write_timeout(Some(timeout)).ok();
    let mut req = format!("{method} {path} HTTP/1.1\r\nHost: {listen}\r\nConnection: close\r\nContent-Length: 0\r\n");
    if let Some(t) = token {
        req.push_str(&format!("Authorization: Bearer {t}\r\n"));
    }
    req.push_str("\r\n");
    s.write_all(req.as_bytes()).map_err(|e| e.to_string())?;
    let mut raw = Vec::new();
    s.read_to_end(&mut raw).map_err(|e| e.to_string())?;
    parse_response(&raw)
}

fn parse_response(raw: &[u8]) -> Result<HttpResponse, String> {
    let sep = raw.windows(4).position(|w| w == b"\r\n\r\n").ok_or("malformed response")?;
    let head = String::from_utf8_lossy(&raw[..sep]).to_string();
    let body_raw = &raw[sep + 4..];
    let status = head
        .lines()
        .next()
        .and_then(|l| l.split_whitespace().nth(1))
        .and_then(|c| c.parse::<u16>().ok())
        .ok_or("malformed status line")?;
    let chunked = head.lines().any(|l| {
        let l = l.to_ascii_lowercase();
        l.starts_with("transfer-encoding:") && l.contains("chunked")
    });
    let body = if chunked { dechunk(body_raw) } else { body_raw.to_vec() };
    Ok(HttpResponse { status, body: String::from_utf8_lossy(&body).to_string() })
}

fn dechunk(mut data: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    while let Some(nl) = data.windows(2).position(|w| w == b"\r\n") {
        let size_str = String::from_utf8_lossy(&data[..nl]);
        let size = usize::from_str_radix(size_str.split(';').next().unwrap_or("").trim(), 16).unwrap_or(0);
        data = &data[nl + 2..];
        if size == 0 || data.len() < size {
            if size > 0 {
                out.extend_from_slice(data);
            }
            break;
        }
        out.extend_from_slice(&data[..size]);
        data = &data[size..];
        if data.starts_with(b"\r\n") {
            data = &data[2..];
        }
    }
    out
}

// ------------------------------------------------------------ discovery

pub fn discover() -> Discovery {
    let mut d = Discovery { data_dir: data_dir().to_string_lossy().to_string(), ..Default::default() };
    match read_service_json() {
        Some(sj) if pid_alive(sj.pid) => {
            d.pid = Some(sj.pid);
            d.listen = Some(sj.listen.clone());
            d.admin_token = Some(sj.admin_token.clone());
            d.managed_by = Some(sj.managed_by.clone());
            d.version = Some(sj.version.clone());
            d.config_path = Some(sj.config_path.clone());
            match http(&sj.listen, "GET", "/api/v1/health", None, Duration::from_millis(1500)) {
                Ok(r) if r.status == 200 => d.running = true,
                Ok(r) => d.error = Some(format!("health check returned HTTP {}", r.status)),
                Err(e) => d.error = Some(format!("health check failed: {e}")),
            }
        }
        _ => {
            d.manager = installed_manager();
        }
    }
    d
}

// ---------------------------------------------------------- launchd/systemd

fn uid() -> u32 {
    // SAFETY: getuid 无副作用。
    unsafe { libc::getuid() }
}

fn launchd_plist() -> PathBuf {
    home_dir().join("Library/LaunchAgents").join(format!("{LAUNCHD_LABEL}.plist"))
}

fn systemd_unit_file() -> PathBuf {
    home_dir().join(".config/systemd/user").join(SYSTEMD_UNIT)
}

/// 服务停止时，检测用户是否安装了 launchd/systemd 服务；使用数据目录覆盖时不检测。
fn installed_manager() -> Option<String> {
    if data_dir_overridden() {
        return None;
    }
    if cfg!(target_os = "macos") && launchd_plist().exists() {
        return Some("launchd".into());
    }
    if cfg!(target_os = "linux") && systemd_unit_file().exists() {
        return Some("systemd".into());
    }
    None
}

fn run_ctl(prog: &str, args: &[&str]) -> Result<(), String> {
    let out = Command::new(prog).args(args).output().map_err(|e| format!("{prog}: {e}"))?;
    if out.status.success() {
        Ok(())
    } else {
        Err(format!("{prog} {}: {}", args.join(" "), String::from_utf8_lossy(&out.stderr).trim()))
    }
}

fn ctl_start(manager: &str) -> Result<(), String> {
    match manager {
        "launchd" => {
            let target = format!("gui/{}/{LAUNCHD_LABEL}", uid());
            if run_ctl("launchctl", &["kickstart", &target]).is_ok() {
                return Ok(());
            }
            let domain = format!("gui/{}", uid());
            run_ctl("launchctl", &["bootstrap", &domain, &launchd_plist().to_string_lossy()])
        }
        _ => run_ctl("systemctl", &["--user", "start", SYSTEMD_UNIT]),
    }
}

fn ctl_stop(manager: &str) -> Result<(), String> {
    match manager {
        "launchd" => {
            let target = format!("gui/{}/{LAUNCHD_LABEL}", uid());
            run_ctl("launchctl", &["bootout", &target])
        }
        _ => run_ctl("systemctl", &["--user", "stop", SYSTEMD_UNIT]),
    }
}

// ------------------------------------------------------------ sidecar paths

fn exe_dir() -> Option<PathBuf> {
    std::env::current_exe().ok()?.parent().map(Path::to_path_buf)
}

fn target_triple() -> String {
    let arch = if cfg!(target_arch = "aarch64") { "aarch64" } else { "x86_64" };
    if cfg!(target_os = "macos") {
        format!("{arch}-apple-darwin")
    } else if cfg!(target_os = "windows") {
        format!("{arch}-pc-windows-msvc")
    } else {
        format!("{arch}-unknown-linux-gnu")
    }
}

fn which(name: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path).map(|d| d.join(name)).find(|p| p.is_file())
}

/// 查找内置 sidecar：环境变量 > 与 UI 可执行文件同目录 > 开发目录 binaries/ > ../build/bin > PATH。
fn find_binary(name: &str, env_key: &str) -> Option<PathBuf> {
    if let Ok(p) = std::env::var(env_key) {
        if !p.is_empty() && Path::new(&p).is_file() {
            return Some(PathBuf::from(p));
        }
    }
    if let Some(dir) = exe_dir() {
        let p = dir.join(name);
        if p.is_file() {
            return Some(p);
        }
        // tauri dev：可执行文件在 src-tauri/target/debug
        for rel in ["../../binaries", "../../../build/bin", "../../../../build/bin"] {
            let c = dir.join(rel);
            let p = c.join(format!("{name}-{}", target_triple()));
            if p.is_file() {
                return Some(p);
            }
            let p = c.join(name);
            if p.is_file() {
                return Some(p);
            }
        }
    }
    which(name)
}

/// macOS 应用包内的 OpenSSL provider 目录：`<App>.app/Contents/Frameworks/ossl-modules`。
fn bundled_ossl_modules() -> Option<PathBuf> {
    let dir = exe_dir()?.join("../Frameworks/ossl-modules");
    dir.is_dir().then(|| dir.canonicalize().unwrap_or(dir))
}

/// 是否能找到 rdp-mcp 可执行文件（设置页提示用）。
pub fn sidecar_available() -> bool {
    find_binary("rdp-mcp", "RDP_MCP_BIN").is_some()
}

pub fn logs_dir() -> PathBuf {
    data_dir().join("logs")
}

/// 在 Finder / 文件管理器中打开日志目录。
pub fn open_logs_dir() -> Result<(), String> {
    let dir = logs_dir();
    fs::create_dir_all(&dir).ok();
    let prog = if cfg!(target_os = "macos") { "open" } else { "xdg-open" };
    Command::new(prog)
        .arg(&dir)
        .status()
        .map_err(|e| format!("{prog}: {e}"))
        .and_then(|s| if s.success() { Ok(()) } else { Err(format!("{prog} exited with {s}")) })
}

// -------------------------------------------------------------- start/stop

pub fn start() -> Result<Discovery, String> {
    INTENTIONAL_STOP.store(false, Ordering::SeqCst);
    let d = discover();
    if d.running {
        return Ok(d);
    }
    if let Some(m) = d.manager.clone() {
        ctl_start(&m)?;
        return wait_running(None, Duration::from_secs(20));
    }

    let bin = find_binary("rdp-mcp", "RDP_MCP_BIN").ok_or("rdp-mcp binary not found (set RDP_MCP_BIN)")?;
    let worker = find_binary("rdp-worker", "RDP_MCP_WORKER");
    let mut cmd = Command::new(&bin);
    cmd.arg("serve").arg("--managed-by").arg("ui-sidecar");
    if let Some(w) = &worker {
        cmd.arg("--worker").arg(w);
    }
    if let Ok(c) = std::env::var("RDP_MCP_CONFIG") {
        if !c.is_empty() {
            cmd.arg("--config").arg(c);
        }
    } else if data_dir_overridden() {
        cmd.arg("--config").arg(data_dir().join("configs.yaml"));
    }
    fs::create_dir_all(logs_dir()).ok();
    // 打包的 OpenSSL provider 模块（legacy.dylib 提供 NTLM 需要的 MD4）。libcrypto 默认到编译时的
    // Homebrew 路径找它，在没有 Homebrew 的机器上找不到会导致 NLA 登录失败，所以显式指过去。
    if std::env::var_os("OPENSSL_MODULES").is_none() {
        if let Some(dir) = bundled_ossl_modules() {
            cmd.env("OPENSSL_MODULES", dir);
        }
    }
    let err_log = fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(logs_dir().join("sidecar.stderr"))
        .map(Stdio::from)
        .unwrap_or_else(|_| Stdio::null());
    cmd.current_dir(home_dir()).stdin(Stdio::null()).stdout(Stdio::null()).stderr(err_log);
    // 脱离 UI：新会话，UI 退出后服务继续运行。
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        // SAFETY: setsid 在 fork 后、exec 前调用，是 async-signal-safe 的。
        unsafe {
            cmd.pre_exec(|| {
                libc::setsid();
                Ok(())
            });
        }
    }
    let mut child = cmd.spawn().map_err(|e| format!("spawn {}: {e}", bin.display()))?;
    let r = wait_running(Some(&mut child), Duration::from_secs(20));
    // 回收子进程，避免 UI 存活期间服务退出后留下僵尸进程。
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    r
}

fn wait_running(mut child: Option<&mut std::process::Child>, timeout: Duration) -> Result<Discovery, String> {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if let Some(c) = child.as_deref_mut() {
            if let Ok(Some(status)) = c.try_wait() {
                return Err(format!("rdp-mcp exited immediately ({status}); see {}", logs_dir().join("sidecar.stderr").display()));
            }
        }
        let d = discover();
        if d.running {
            return Ok(d);
        }
        std::thread::sleep(Duration::from_millis(200));
    }
    Err("timed out waiting for the service to become healthy".into())
}

fn wait_exit(pid: i32, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if !pid_alive(pid) {
            return true;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    !pid_alive(pid)
}

pub fn stop() -> Result<Discovery, String> {
    INTENTIONAL_STOP.store(true, Ordering::SeqCst);
    let Some(sj) = read_service_json().filter(|s| pid_alive(s.pid)) else {
        return Ok(discover());
    };
    if sj.managed_by == "launchd" || sj.managed_by == "systemd" {
        ctl_stop(&sj.managed_by)?;
        if !wait_exit(sj.pid, Duration::from_secs(10)) {
            return Err("service did not stop".into());
        }
        return Ok(discover());
    }
    // 1) 优雅关闭
    let _ = http(&sj.listen, "POST", "/api/v1/service/shutdown", Some(&sj.admin_token), Duration::from_secs(3));
    if !wait_exit(sj.pid, Duration::from_secs(8)) {
        // 2) SIGTERM
        // SAFETY: 向已确认存在的服务进程发信号。
        unsafe { libc::kill(sj.pid, libc::SIGTERM) };
        if !wait_exit(sj.pid, Duration::from_secs(5)) {
            // 3) SIGKILL，并清理残留的运行时文件
            unsafe { libc::kill(sj.pid, libc::SIGKILL) };
            wait_exit(sj.pid, Duration::from_secs(2));
            let _ = fs::remove_file(data_dir().join("run").join("service.json"));
        }
    }
    if pid_alive(sj.pid) {
        return Err("service did not stop".into());
    }
    Ok(discover())
}

pub fn restart() -> Result<Discovery, String> {
    stop()?;
    start()
}

/// 紧急停止：所有机器进入 paused。
pub fn emergency_stop() -> Result<(), String> {
    let d = discover();
    let (Some(listen), Some(tok)) = (d.listen, d.admin_token) else {
        return Err("service not running".into());
    };
    let r = http(&listen, "POST", "/api/v1/control/emergency-stop", Some(&tok), Duration::from_secs(3))?;
    if r.status / 100 == 2 {
        Ok(())
    } else {
        Err(format!("HTTP {}", r.status))
    }
}

/// 机器概况（托盘用）：(已连接数, 是否有失败/暂停)。
pub fn machine_summary(d: &Discovery) -> Option<(usize, bool)> {
    let (listen, tok) = (d.listen.as_ref()?, d.admin_token.as_ref()?);
    let r = http(listen, "GET", "/api/v1/machines", Some(tok), Duration::from_secs(2)).ok()?;
    if r.status != 200 {
        return None;
    }
    let v: serde_json::Value = serde_json::from_str(&r.body).ok()?;
    let arr = v.as_array()?;
    let connected = arr.iter().filter(|m| m["state"] == "connected").count();
    let attention = arr.iter().any(|m| m["state"] == "failed" || m["control_mode"] == "paused");
    Some((connected, attention))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_plain_response() {
        let r = parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok").unwrap();
        assert_eq!(r.status, 200);
        assert_eq!(r.body, "ok");
    }

    #[test]
    fn parses_chunked_response() {
        let r = parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n").unwrap();
        assert_eq!(r.body, "hello world");
    }

    #[test]
    fn rejects_garbage() {
        assert!(parse_response(b"nope").is_err());
    }
}
