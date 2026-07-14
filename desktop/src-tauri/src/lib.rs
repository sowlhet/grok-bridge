mod paths;
mod process;

use std::sync::Mutex;

use process::SidecarManager;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, RunEvent, WindowEvent};
use tauri_plugin_autostart::MacosLauncher;

const TRAY_SHOW: &str = "tray_show";
const TRAY_START: &str = "tray_start";
const TRAY_STOP: &str = "tray_stop";
const TRAY_RESTART: &str = "tray_restart";
const TRAY_OPEN_DATA: &str = "tray_open_data";
const TRAY_QUIT: &str = "tray_quit";

struct AppState {
    sidecar: Mutex<SidecarManager>,
}

#[tauri::command]
fn service_status(state: tauri::State<'_, AppState>) -> String {
    state
        .sidecar
        .lock()
        .map(|s| format!("{:?}", s.state()))
        .unwrap_or_else(|_| "unknown".into())
}

#[tauri::command]
fn service_base_url(state: tauri::State<'_, AppState>) -> String {
    state
        .sidecar
        .lock()
        .map(|s| s.base_url())
        .unwrap_or_else(|_| "http://127.0.0.1:18080".into())
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.set_focus();
    }
}

fn navigate_main_to_admin(app: &AppHandle, url: &str) {
    if let Some(window) = app.get_webview_window("main") {
        let js_url = serde_json::to_string(url).unwrap_or_else(|_| "\"about:blank\"".into());
        let _ = window.eval(&format!("window.location.replace({js_url})"));
    }
}

fn start_service(app: &AppHandle) -> Result<(), String> {
    let state = app.state::<AppState>();
    let mut mgr = state.sidecar.lock().map_err(|e| e.to_string())?;
    mgr.start()?;
    let admin = mgr.admin_url();
    drop(mgr);
    // Navigate immediately and once more shortly after, in case WebView wasn't ready.
    navigate_main_to_admin(app, &admin);
    show_main_window(app);
    let app2 = app.clone();
    let admin2 = admin.clone();
    std::thread::spawn(move || {
        std::thread::sleep(std::time::Duration::from_millis(500));
        navigate_main_to_admin(&app2, &admin2);
        std::thread::sleep(std::time::Duration::from_millis(1200));
        navigate_main_to_admin(&app2, &admin2);
    });
    Ok(())
}

fn stop_service(app: &AppHandle) -> Result<(), String> {
    let state = app.state::<AppState>();
    let mut mgr = state.sidecar.lock().map_err(|e| e.to_string())?;
    mgr.stop();
    Ok(())
}

fn restart_service(app: &AppHandle) -> Result<(), String> {
    stop_service(app)?;
    start_service(app)
}

fn open_data_dir() -> Result<(), String> {
    let dir = paths::data_dir()?;
    // Ensure directory exists before revealing it.
    let _ = paths::data_dir()?;
    open::that(&dir).map_err(|e| format!("open data dir {}: {e}", dir.display()))
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, TRAY_SHOW, "打开管理面板", true, None::<&str>)?;
    let start = MenuItem::with_id(app, TRAY_START, "启动服务", true, None::<&str>)?;
    let stop = MenuItem::with_id(app, TRAY_STOP, "停止服务", true, None::<&str>)?;
    let restart = MenuItem::with_id(app, TRAY_RESTART, "重启服务", true, None::<&str>)?;
    let open_data = MenuItem::with_id(app, TRAY_OPEN_DATA, "打开数据目录", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, TRAY_QUIT, "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &start, &stop, &restart, &open_data, &quit])?;

    let icon = app
        .default_window_icon()
        .cloned()
        .expect("missing app icon");

    let _tray = TrayIconBuilder::new()
        .menu(&menu)
        .tooltip("Grok Bridge")
        .icon(icon)
        .on_menu_event(|app, event| match event.id.as_ref() {
            TRAY_SHOW => show_main_window(app),
            TRAY_START => {
                if let Err(err) = start_service(app) {
                    eprintln!("start service failed: {err}");
                }
            }
            TRAY_STOP => {
                if let Err(err) = stop_service(app) {
                    eprintln!("stop service failed: {err}");
                }
            }
            TRAY_RESTART => {
                if let Err(err) = restart_service(app) {
                    eprintln!("restart service failed: {err}");
                }
            }
            TRAY_OPEN_DATA => {
                if let Err(err) = open_data_dir() {
                    eprintln!("open data dir failed: {err}");
                }
            }
            TRAY_QUIT => {
                let _ = stop_service(app);
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                show_main_window(tray.app_handle());
            }
        })
        .build(app)?;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_main_window(app);
        }))
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            Some(vec![]),
        ))
        .manage(AppState {
            sidecar: Mutex::new(SidecarManager::new()),
        })
        .invoke_handler(tauri::generate_handler![service_status, service_base_url])
        .setup(|app| {
            setup_tray(app.handle())?;
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                std::thread::sleep(std::time::Duration::from_millis(300));
                if let Err(err) = start_service(&handle) {
                    eprintln!("auto start failed: {err}");
                }
            });
            Ok(())
        })
        .on_window_event(|window, event| {
            if window.label() == "main" {
                if let WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building Grok Bridge desktop")
        .run(|app_handle, event| {
            if let RunEvent::Exit = event {
                let _ = stop_service(app_handle);
            }
        });
}
