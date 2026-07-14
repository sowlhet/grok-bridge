use std::fs::OpenOptions;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[cfg(windows)]
use std::os::windows::process::CommandExt;

use crate::paths;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ServiceState {
    Stopped,
    Starting,
    Running,
    Error,
}

pub struct SidecarManager {
    child: Option<Child>,
    state: ServiceState,
    port: u16,
    base_url: String,
    last_error: String,
    desktop_token: String,
}

impl Default for SidecarManager {
    fn default() -> Self {
        Self::new()
    }
}

impl SidecarManager {
    pub fn new() -> Self {
        let port = 18080;
        Self {
            child: None,
            state: ServiceState::Stopped,
            port,
            base_url: format!("http://127.0.0.1:{port}"),
            last_error: String::new(),
            desktop_token: String::new(),
        }
    }

    pub fn state(&self) -> ServiceState {
        self.state
    }

    pub fn base_url(&self) -> String {
        self.base_url.clone()
    }

    pub fn admin_url(&self) -> String {
        format!("{}/admin/", self.base_url.trim_end_matches('/'))
    }

    pub fn desktop_token(&self) -> String {
        self.desktop_token.clone()
    }

    pub fn start(&mut self) -> Result<(), String> {
        if matches!(self.state, ServiceState::Running | ServiceState::Starting) {
            if self.health_ok() {
                self.state = ServiceState::Running;
                return Ok(());
            }
        }
        self.stop();

        self.state = ServiceState::Starting;
        let listen = format!("127.0.0.1:{}", self.port);
        let password = std::env::var("GROK_BRIDGE_ADMIN_PASSWORD")
            .unwrap_or_else(|_| "grok-bridge-dev".to_string());
        let desktop_token = ensure_desktop_token()?;
        let config = paths::ensure_config(&listen, &password)?;
        let bin = resolve_sidecar_binary()?;
        let log_path = paths::data_dir()?.join("logs").join("sidecar.log");
        let log_file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&log_path)
            .map_err(|e| format!("open sidecar log: {e}"))?;
        let err_file = log_file
            .try_clone()
            .map_err(|e| format!("clone sidecar log: {e}"))?;

        let mut cmd = Command::new(&bin);
        cmd.arg("-config")
            .arg(&config)
            .env("GROK_BRIDGE_LISTEN", &listen)
            .env("GROK_BRIDGE_ADMIN_PASSWORD", &password)
            .env("GROK_BRIDGE_DESKTOP_TOKEN", &desktop_token)
            .stdin(Stdio::null())
            .stdout(Stdio::from(log_file))
            .stderr(Stdio::from(err_file));
        // Prevent a black console window for the Go sidecar on Windows.
        #[cfg(windows)]
        {
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }

        let child = cmd
            .spawn()
            .map_err(|e| format!("spawn sidecar {}: {e}", bin.display()))?;
        self.child = Some(child);
        self.base_url = format!("http://127.0.0.1:{}", self.port);
        self.desktop_token = desktop_token;

        // Wait for health.
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if self.health_ok() {
                self.state = ServiceState::Running;
                return Ok(());
            }
            // If process already exited, fail fast.
            if let Some(child) = self.child.as_mut() {
                if let Ok(Some(status)) = child.try_wait() {
                    self.state = ServiceState::Error;
                    self.last_error = format!("sidecar exited early: {status}");
                    return Err(self.last_error.clone());
                }
            }
            thread::sleep(Duration::from_millis(250));
        }
        self.state = ServiceState::Error;
        self.last_error = "sidecar health check timeout".into();
        Err(self.last_error.clone())
    }

    pub fn stop(&mut self) {
        if let Some(mut child) = self.child.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
        self.state = ServiceState::Stopped;
    }

    fn health_ok(&self) -> bool {
        let client = match reqwest::blocking::Client::builder()
            .timeout(Duration::from_secs(2))
            .redirect(reqwest::redirect::Policy::limited(5))
            .build()
        {
            Ok(c) => c,
            Err(_) => return false,
        };
        let health = format!("{}/healthz", self.base_url.trim_end_matches('/'));
        let admin = format!("{}/admin/", self.base_url.trim_end_matches('/'));
        let health_ok = client
            .get(&health)
            .send()
            .map(|r| r.status().is_success())
            .unwrap_or(false);
        if !health_ok {
            return false;
        }
        // Ensure admin UI is actually ready (avoid navigating to a 404 root page).
        client
            .get(&admin)
            .send()
            .map(|r| r.status().is_success())
            .unwrap_or(false)
    }
}

impl Drop for SidecarManager {
    fn drop(&mut self) {
        self.stop();
    }
}

fn resolve_sidecar_binary() -> Result<PathBuf, String> {
    // 1) Explicit env override
    if let Ok(p) = std::env::var("GROK_BRIDGE_SIDECAR") {
        let path = PathBuf::from(p);
        if path.exists() {
            return Ok(path);
        }
    }

    // 2) Bundled resource: resources/grok-bridge(.exe)
    if let Ok(res) = std::env::current_exe() {
        let mut candidates = vec![];
        if let Some(dir) = res.parent() {
            candidates.push(dir.join("grok-bridge"));
            candidates.push(dir.join("grok-bridge.exe"));
            // Tauri externalBin naming
            candidates.push(dir.join("grok-bridge-x86_64-pc-windows-msvc.exe"));
            candidates.push(dir.join("grok-bridge-aarch64-apple-darwin"));
            candidates.push(dir.join("grok-bridge-x86_64-apple-darwin"));
            candidates.push(dir.join("resources").join("grok-bridge"));
            candidates.push(dir.join("resources").join("grok-bridge.exe"));
            // macOS app bundle Resources
            candidates.push(dir.join("../Resources/grok-bridge"));
            candidates.push(dir.join("../Resources/resources/grok-bridge"));
        }
        for c in candidates {
            if c.exists() {
                return Ok(c);
            }
        }
    }

    // 3) Dev fallback: repo build outputs
    let dev_candidates = [
        Path::new("../grok-bridge/grok-bridge"),
        Path::new("../../grok-bridge/grok-bridge"),
        Path::new("../../../grok-bridge/grok-bridge"),
        Path::new("./grok-bridge"),
    ];
    for c in dev_candidates {
        if c.exists() {
            return Ok(c.to_path_buf());
        }
    }

    // 4) PATH
    if let Ok(path) = which("grok-bridge") {
        return Ok(path);
    }

    Err("cannot find grok-bridge sidecar binary; set GROK_BRIDGE_SIDECAR or build ../grok-bridge first".into())
}

fn which(name: &str) -> Result<PathBuf, ()> {
    if let Ok(path_var) = std::env::var("PATH") {
        for dir in std::env::split_paths(&path_var) {
            let p = dir.join(name);
            if p.exists() {
                return Ok(p);
            }
            let p_exe = dir.join(format!("{name}.exe"));
            if p_exe.exists() {
                return Ok(p_exe);
            }
        }
    }
    Err(())
}

// silence unused import warning on some platforms
#[allow(dead_code)]
fn _write_log(path: &Path, line: &str) {
    if let Ok(mut f) = OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(f, "{line}");
    }
}


fn ensure_desktop_token() -> Result<String, String> {
    if let Ok(v) = std::env::var("GROK_BRIDGE_DESKTOP_TOKEN") {
        let v = v.trim().to_string();
        if !v.is_empty() {
            return Ok(v);
        }
    }
    let path = paths::data_dir()?.join("desktop.token");
    if path.exists() {
        let v = std::fs::read_to_string(&path).map_err(|e| format!("read desktop token: {e}"))?;
        let v = v.trim().to_string();
        if !v.is_empty() {
            return Ok(v);
        }
    }
    // generate random token
    let mut buf = [0u8; 32];
    // simple entropy from time + randomish values
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    for (i, b) in buf.iter_mut().enumerate() {
        *b = ((now >> ((i % 16) * 4)) as u8).wrapping_add((i as u8).wrapping_mul(37));
    }
    // better: use getrandom via std if available; fall back ok for local desktop
    #[cfg(any(unix, windows))]
    {
        use std::hash::{Hash, Hasher};
        let mut h = std::collections::hash_map::DefaultHasher::new();
        now.hash(&mut h);
        std::process::id().hash(&mut h);
        let hv = h.finish().to_le_bytes();
        for i in 0..8 {
            buf[i] ^= hv[i];
            buf[i + 8] ^= hv[i].wrapping_mul(3);
            buf[i + 16] ^= hv[i].wrapping_add(11);
            buf[i + 24] ^= hv[i].wrapping_add(29);
        }
    }
    let token = buf.iter().map(|b| format!("{b:02x}")).collect::<String>();
    std::fs::write(&path, &token).map_err(|e| format!("write desktop token: {e}"))?;
    Ok(token)
}
