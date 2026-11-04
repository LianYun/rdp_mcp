//! 应用级设置（仅 UI 自身的偏好，不是 remoter-mcp 的配置；服务配置只通过管理 API 修改）。

use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;
use tauri::{AppHandle, Manager};

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(default)]
pub struct AppSettings {
    /// 启动 UI 时，若没有运行中的服务则自动启动。
    pub auto_start_service: bool,
    /// 退出 UI 后服务继续在后台运行（仅对由 UI 启动的服务生效）。
    pub keep_service_running: bool,
    /// 关闭主窗口时最小化到托盘。
    pub close_to_tray: bool,
    /// 托盘菜单语言：zh | en。
    pub language: String,
}

impl Default for AppSettings {
    fn default() -> Self {
        Self { auto_start_service: true, keep_service_running: true, close_to_tray: true, language: "zh".into() }
    }
}

pub struct SettingsState(pub Mutex<AppSettings>);

fn path(app: &AppHandle) -> Option<PathBuf> {
    if crate::service::data_dir_overridden() {
        return Some(crate::service::data_dir().join("ui-settings.json"));
    }
    app.path().app_config_dir().ok().map(|d| d.join("settings.json"))
}

pub fn load(app: &AppHandle) -> AppSettings {
    path(app)
        .and_then(|p| fs::read(p).ok())
        .and_then(|b| serde_json::from_slice(&b).ok())
        .unwrap_or_default()
}

pub fn save(app: &AppHandle, s: &AppSettings) -> Result<(), String> {
    let p = path(app).ok_or("no config dir")?;
    if let Some(dir) = p.parent() {
        fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    fs::write(p, serde_json::to_vec_pretty(s).map_err(|e| e.to_string())?).map_err(|e| e.to_string())
}

pub fn current(app: &AppHandle) -> AppSettings {
    app.state::<SettingsState>().0.lock().unwrap().clone()
}
