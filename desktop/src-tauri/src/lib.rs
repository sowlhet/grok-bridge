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

#[tauri::command]
fn get_listen_port(state: tauri::State<'_, AppState>) -> u16 {
    state.sidecar.lock().map(|s| s.port()).unwrap_or(18080)
}

#[tauri::command]
fn set_listen_port(app: AppHandle, state: tauri::State<'_, AppState>, port: u16) -> Result<String, String> {
    if !(1024..=65535).contains(&port) {
        return Err("端口需在 1024-65535".into());
    }
    {
        let mut mgr = state.sidecar.lock().map_err(|e| e.to_string())?;
        mgr.set_port(port)?;
    }
    // Restart service so new port binds immediately.
    restart_service(&app)?;
    let base = state
        .sidecar
        .lock()
        .map(|s| s.base_url())
        .unwrap_or_else(|_| format!("http://127.0.0.1:{port}"));
    Ok(base)
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        // Only show/focus — never reload page here (prevents tray flicker).
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn navigate_main_to_admin(app: &AppHandle, url: &str) {
    let Some(window) = app.get_webview_window("main") else { return; };
    let Ok(url_json) = serde_json::to_string(url) else { return; };
    // Navigate only if not already on /admin to avoid white-flash reloads.
    let mut js = String::new();
    js.push_str("(function(){");
    js.push_str("var target=");
    js.push_str(&url_json);
    js.push(';');
    js.push_str("var href=String(location.href||\"\");");
    js.push_str("if(href.indexOf(\"/admin\")>=0){return;}");
    js.push_str("location.replace(target);");
    js.push_str("})();");
    let _ = window.eval(&js);
}

fn start_service(app: &AppHandle) -> Result<(), String> {
    let state = app.state::<AppState>();
    let mut mgr = state.sidecar.lock().map_err(|e| e.to_string())?;
    let already_running = matches!(mgr.state(), process::ServiceState::Running);
    mgr.start()?;
    let admin = mgr.admin_url();
    let token = mgr.desktop_token();
    drop(mgr);

    // First boot only: open admin + silent login once.
    // Later tray clicks only show the existing window (no reload).
    if !already_running {
        navigate_main_to_admin(app, &admin);
        if !token.is_empty() {
            let app2 = app.clone();
            let admin2 = admin.clone();
            let token2 = token.clone();
            std::thread::spawn(move || {
                std::thread::sleep(std::time::Duration::from_millis(300));
                silent_login_and_open(&app2, &admin2, &token2);
            });
        }
    }
    show_main_window(app);
    Ok(())
}

fn silent_login_and_open(app: &AppHandle, admin_url: &str, desktop_token: &str) {
    let Some(window) = app.get_webview_window("main") else { return; };
    let Ok(admin_json) = serde_json::to_string(admin_url) else { return; };
    let Ok(token_json) = serde_json::to_string(desktop_token) else { return; };
    let base = admin_url.trim_end_matches('/').trim_end_matches("/admin").to_string();
    let login_url = format!("{}/admin/api/desktop-login", base.trim_end_matches('/'));
    let Ok(login_json) = serde_json::to_string(&login_url) else { return; };

    let mut js = String::new();
    js.push_str("(async()=>{");
    js.push_str("if(window.__gbDesktopAuthed){return;}");
    js.push_str("const loginApi="); js.push_str(&login_json); js.push(';');
    js.push_str("const adminUrl="); js.push_str(&admin_json); js.push(';');
    js.push_str("const token="); js.push_str(&token_json); js.push(';');
    js.push_str("try{");
    js.push_str("const res=await fetch(loginApi,{method:\"POST\",credentials:\"include\",headers:{\"Content-Type\":\"application/json\",\"X-Grok-Bridge-Desktop-Token\":token},body:JSON.stringify({token})});");
    js.push_str("if(!res.ok){console.warn(\"desktop silent login failed\",res.status);return;}");
    js.push_str("window.__gbDesktopAuthed=true;");
    js.push_str("var href=String(location.href||\"\");");
    js.push_str("if(href.indexOf(\"/admin\")<0){location.replace(adminUrl);}");
    js.push_str("else if(document.querySelector(\"input[type=password]\")){location.replace(adminUrl);}");
    js.push_str("}catch(e){console.warn(\"desktop silent login error\",e);}");
    js.push_str("})();");
    let _ = window.eval(&js);
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
        .invoke_handler(tauri::generate_handler![service_status, service_base_url, get_listen_port, set_listen_port])
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
