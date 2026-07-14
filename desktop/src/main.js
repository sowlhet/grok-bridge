const statusEl = document.getElementById("status");
async function refresh() {
  try {
    const invoke = window.__TAURI__?.core?.invoke;
    if (!invoke) {
      statusEl.textContent = "等待桌面运行时…";
      return;
    }
    const status = await invoke("service_status");
    const base = await invoke("service_base_url");
    statusEl.textContent = `服务状态：${status}（${base}）`;
  } catch (e) {
    statusEl.textContent = `启动中：${e}`;
  }
}
refresh();
setInterval(refresh, 1000);
