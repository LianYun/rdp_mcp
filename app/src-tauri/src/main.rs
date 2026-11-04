// 发布构建在 Windows 上不弹控制台窗口（目前仅 macOS/Linux 目标，保留以防后续）。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod service;
mod settings;
mod tray;

use settings::{AppSettings, SettingsState};
use std::sync::Mutex;
use tauri::ipc::{InvokeBody, Request};
use tauri::{AppHandle, Manager, RunEvent, WindowEvent};
use tauri_plugin_autostart::MacosLauncher;

pub fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

// ------------------------------------------------------------- commands

#[tauri::command]
async fn service_discover() -> service::Discovery {
    tauri::async_runtime::spawn_blocking(service::discover).await.unwrap_or_default()
}

#[tauri::command]
async fn service_start() -> Result<service::Discovery, String> {
    tauri::async_runtime::spawn_blocking(service::start).await.map_err(|e| e.to_string())?
}

#[tauri::command]
async fn service_stop() -> Result<service::Discovery, String> {
    tauri::async_runtime::spawn_blocking(service::stop).await.map_err(|e| e.to_string())?
}

#[tauri::command]
async fn service_restart() -> Result<service::Discovery, String> {
    tauri::async_runtime::spawn_blocking(service::restart).await.map_err(|e| e.to_string())?
}

#[tauri::command]
fn service_logs_path() -> String {
    service::logs_dir().to_string_lossy().to_string()
}

#[tauri::command]
async fn service_open_logs() -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(service::open_logs_dir).await.map_err(|e| e.to_string())?
}

#[tauri::command]
fn app_settings_get(state: tauri::State<SettingsState>) -> AppSettings {
    state.0.lock().unwrap().clone()
}

#[tauri::command]
fn app_settings_set(app: AppHandle, state: tauri::State<SettingsState>, value: AppSettings) -> Result<(), String> {
    settings::save(&app, &value)?;
    *state.0.lock().unwrap() = value;
    tray::relabel(&app);
    Ok(())
}

#[derive(serde::Serialize)]
struct AppInfo {
    version: String,
    os: &'static str,
    data_dir: String,
    data_dir_overridden: bool,
    sidecar_found: bool,
}

#[tauri::command]
fn app_info(app: AppHandle) -> AppInfo {
    AppInfo {
        version: app.package_info().version.to_string(),
        os: std::env::consts::OS,
        data_dir: service::data_dir().to_string_lossy().to_string(),
        data_dir_overridden: service::data_dir_overridden(),
        sidecar_found: service::sidecar_available(),
    }
}

/// 保存二进制文件（截图）。请求体为原始字节，目标路径放在 `path` 头里（URL 编码）。
#[tauri::command]
fn save_file(request: Request<'_>) -> Result<(), String> {
    let InvokeBody::Raw(data) = request.body() else {
        return Err("expected raw body".into());
    };
    let path = request
        .headers()
        .get("path")
        .and_then(|v| v.to_str().ok())
        .ok_or("missing path header")?;
    let path = percent_decode(path);
    std::fs::write(path, data).map_err(|e| e.to_string())
}

fn percent_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 3 <= b.len() {
            if let Ok(v) = u8::from_str_radix(&s[i + 1..i + 3], 16) {
                out.push(v);
                i += 3;
                continue;
            }
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).to_string()
}

// ------------------------------------------------------------------ run

fn main() {
    let mut builder = tauri::Builder::default();
    // 使用数据目录覆盖（测试/多实例）时不启用单实例，避免与用户真实运行的 UI 互相转发。
    if !service::data_dir_overridden() {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| show_main(app)));
    }
    builder
        .plugin(tauri_plugin_autostart::init(MacosLauncher::LaunchAgent, Some(vec!["--minimized"])))
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![
            service_discover,
            service_start,
            service_stop,
            service_restart,
            service_logs_path,
            service_open_logs,
            app_settings_get,
            app_settings_set,
            app_info,
            save_file
        ])
        .setup(|app| {
            let handle = app.handle().clone();
            let s = settings::load(&handle);
            app.manage(SettingsState(Mutex::new(s.clone())));
            app.manage(tray::TrayState::new());
            tray::setup(app)?;
            tray::spawn_monitor(handle.clone());

            if s.auto_start_service {
                tauri::async_runtime::spawn_blocking(|| {
                    // 已有服务则附着；否则用内置 sidecar 启动。失败由前端的状态卡片展示。
                    let _ = service::start();
                });
            }
            if std::env::args().any(|a| a == "--minimized") {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.hide();
                }
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                if settings::current(window.app_handle()).close_to_tray {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| match event {
            #[cfg(target_os = "macos")]
            RunEvent::Reopen { .. } => show_main(app),
            RunEvent::Exit => {
                // “随 UI 退出”：只停止由 UI 自己启动的服务。
                if !settings::current(app).keep_service_running {
                    let d = service::discover();
                    if d.running && d.managed_by.as_deref() == Some("ui-sidecar") {
                        let _ = service::stop();
                    }
                }
            }
            _ => {
                let _ = app;
            }
        });
}
