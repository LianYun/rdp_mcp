//! 系统托盘：状态灯、启动/停止/重启服务、紧急停止、打开主窗口、退出。

use crate::{service, settings};
use std::sync::Mutex;
use std::time::Duration;
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, Wry};
use tauri_plugin_notification::NotificationExt;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Light {
    Running,
    Attention,
    Stopped,
    Crashed,
}

struct Items {
    status: MenuItem<Wry>,
    open: MenuItem<Wry>,
    start: MenuItem<Wry>,
    stop: MenuItem<Wry>,
    restart: MenuItem<Wry>,
    emergency: MenuItem<Wry>,
    resume: MenuItem<Wry>,
    quit: MenuItem<Wry>,
}

pub struct TrayState(Mutex<Option<Items>>);

impl TrayState {
    pub fn new() -> Self {
        Self(Mutex::new(None))
    }
}

fn tr(lang: &str, key: &str) -> &'static str {
    let zh = lang != "en";
    match (key, zh) {
        ("open", true) => "打开主窗口",
        ("open", false) => "Open window",
        ("start", true) => "启动服务",
        ("start", false) => "Start service",
        ("stop", true) => "停止服务",
        ("stop", false) => "Stop service",
        ("restart", true) => "重启服务",
        ("restart", false) => "Restart service",
        ("emergency", true) => "紧急停止（全部暂停）",
        ("emergency", false) => "Emergency stop (pause all)",
        ("resume", true) => "全部恢复（交还给模型）",
        ("resume", false) => "Resume all (hand back to model)",
        ("quit", true) => "退出",
        ("quit", false) => "Quit",
        ("running", true) => "服务运行中",
        ("running", false) => "Service running",
        ("stopped", true) => "服务已停止",
        ("stopped", false) => "Service stopped",
        ("crashed", true) => "服务异常",
        ("crashed", false) => "Service error",
        _ => "",
    }
}

/// 生成纯色圆点图标，避免额外的图片资源。
fn dot_icon(light: Light) -> Image<'static> {
    let (r, g, b) = match light {
        Light::Running => (0x22, 0xc5, 0x5e),
        Light::Attention => (0xf5, 0x9e, 0x0b),
        Light::Stopped => (0x9c, 0xa3, 0xaf),
        Light::Crashed => (0xef, 0x44, 0x44),
    };
    let size = 32u32;
    let mut px = vec![0u8; (size * size * 4) as usize];
    let c = (size as f32 - 1.0) / 2.0;
    for y in 0..size {
        for x in 0..size {
            let d = ((x as f32 - c).powi(2) + (y as f32 - c).powi(2)).sqrt();
            let a = (13.0 - d).clamp(0.0, 1.0); // 半径 ~12.5，带 1px 抗锯齿
            let i = ((y * size + x) * 4) as usize;
            px[i] = r;
            px[i + 1] = g;
            px[i + 2] = b;
            px[i + 3] = (a * 255.0) as u8;
        }
    }
    Image::new_owned(px, size, size)
}

pub fn setup(app: &tauri::App) -> tauri::Result<()> {
    let handle = app.handle();
    let lang = settings::current(handle).language;
    let status = MenuItem::with_id(app, "status", tr(&lang, "stopped"), false, None::<&str>)?;
    let open = MenuItem::with_id(app, "open", tr(&lang, "open"), true, None::<&str>)?;
    let start = MenuItem::with_id(app, "start", tr(&lang, "start"), true, None::<&str>)?;
    let stop = MenuItem::with_id(app, "stop", tr(&lang, "stop"), true, None::<&str>)?;
    let restart = MenuItem::with_id(app, "restart", tr(&lang, "restart"), true, None::<&str>)?;
    let emergency = MenuItem::with_id(app, "emergency", tr(&lang, "emergency"), true, None::<&str>)?;
    let resume = MenuItem::with_id(app, "resume", tr(&lang, "resume"), true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", tr(&lang, "quit"), true, None::<&str>)?;
    let sep1 = PredefinedMenuItem::separator(app)?;
    let sep2 = PredefinedMenuItem::separator(app)?;
    let menu = Menu::with_items(app, &[&status, &sep1, &open, &start, &stop, &restart, &emergency, &resume, &sep2, &quit])?;

    TrayIconBuilder::with_id("main")
        .icon(dot_icon(Light::Stopped))
        .tooltip("Remoter")
        .menu(&menu)
        .show_menu_on_left_click(true)
        .on_menu_event(|app, ev| match ev.id.as_ref() {
            "open" => crate::show_main(app),
            "start" => spawn_action(app, "start"),
            "stop" => spawn_action(app, "stop"),
            "restart" => spawn_action(app, "restart"),
            "emergency" => spawn_action(app, "emergency"),
            "resume" => spawn_action(app, "resume"),
            "quit" => app.exit(0),
            _ => {}
        })
        .build(app)?;

    *app.state::<TrayState>().0.lock().unwrap() = Some(Items { status, open, start, stop, restart, emergency, resume, quit });
    Ok(())
}

fn spawn_action(app: &AppHandle, action: &'static str) {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let r = match action {
            "start" => service::start().map(|_| ()),
            "stop" => service::stop().map(|_| ()),
            "restart" => service::restart().map(|_| ()),
            "resume" => service::resume_all(),
            _ => service::emergency_stop(),
        };
        if let Err(e) = r {
            let _ = app.notification().builder().title("Remoter").body(e).show();
        }
    });
}

/// 语言切换后刷新静态菜单文案。
pub fn relabel(app: &AppHandle) {
    let lang = settings::current(app).language;
    if let Some(it) = app.state::<TrayState>().0.lock().unwrap().as_ref() {
        let _ = it.open.set_text(tr(&lang, "open"));
        let _ = it.start.set_text(tr(&lang, "start"));
        let _ = it.stop.set_text(tr(&lang, "stop"));
        let _ = it.restart.set_text(tr(&lang, "restart"));
        let _ = it.emergency.set_text(tr(&lang, "emergency"));
        let _ = it.resume.set_text(tr(&lang, "resume"));
        let _ = it.quit.set_text(tr(&lang, "quit"));
    }
}

fn apply(app: &AppHandle, light: Light, connected: Option<usize>) {
    let lang = settings::current(app).language;
    let label = match light {
        Light::Running | Light::Attention => tr(&lang, "running"),
        Light::Stopped => tr(&lang, "stopped"),
        Light::Crashed => tr(&lang, "crashed"),
    };
    let text = match connected {
        Some(n) if light == Light::Running || light == Light::Attention => {
            if lang == "en" { format!("{label} · {n} connected") } else { format!("{label} · 已连接 {n} 台") }
        }
        _ => label.to_string(),
    };
    if let Some(tray) = app.tray_by_id("main") {
        let _ = tray.set_icon(Some(dot_icon(light)));
        let _ = tray.set_tooltip(Some(format!("Remoter — {text}")));
    }
    if let Some(it) = app.state::<TrayState>().0.lock().unwrap().as_ref() {
        let _ = it.status.set_text(text);
        let running = light == Light::Running || light == Light::Attention;
        let _ = it.start.set_enabled(!running);
        let _ = it.stop.set_enabled(running);
        let _ = it.restart.set_enabled(running);
        let _ = it.emergency.set_enabled(running);
        let _ = it.resume.set_enabled(running);
    }
}

/// 后台线程：每 3 秒刷新托盘状态；服务意外退出时发系统通知。
pub fn spawn_monitor(app: AppHandle) {
    std::thread::spawn(move || {
        let mut was_running = false;
        let mut crashed = false;
        loop {
            let d = service::discover();
            if d.running {
                was_running = true;
                crashed = false;
                match service::machine_summary(&d) {
                    Some((n, attention)) => apply(&app, if attention { Light::Attention } else { Light::Running }, Some(n)),
                    None => apply(&app, Light::Running, None),
                }
            } else {
                if service::INTENTIONAL_STOP.load(std::sync::atomic::Ordering::SeqCst) {
                    was_running = false;
                    crashed = false;
                } else if was_running {
                    // 之前在运行、现在没了、且不是用户主动停止：视为异常退出
                    was_running = false;
                    crashed = true;
                    let lang = settings::current(&app).language;
                    let _ = app.notification().builder().title("Remoter").body(tr(&lang, "crashed")).show();
                }
                apply(&app, if crashed { Light::Crashed } else { Light::Stopped }, None);
            }
            std::thread::sleep(Duration::from_secs(3));
        }
    });
}
