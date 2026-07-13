/* Grok Bridge Admin SPA — 中文界面，无构建步骤 */
(function () {
  "use strict";

  const API = "/admin/api";
  const app = document.getElementById("app");

  const state = {
    authed: false,
    page: "dashboard",
    loading: false,
    error: "",
    flash: "",
    dashboard: null,
    accounts: [],
    keys: [],
    logs: [],
    settings: null,
    logFilters: { from: "", to: "", account_id: "", model: "", status: "", protocol: "" },
    selectedLog: null,
    modal: null,
  };

  // 防止快速切换菜单时旧请求覆盖新页面
  let loadSeq = 0;

  // ---------- helpers ----------
  async function api(path, opts = {}) {
    const headers = Object.assign({}, opts.headers || {});
    if (opts.body && !(opts.body instanceof FormData) && !headers["Content-Type"]) {
      headers["Content-Type"] = "application/json";
    }
    const res = await fetch(API + path, {
      credentials: "include",
      ...opts,
      headers,
    });
    const ct = res.headers.get("content-type") || "";
    let data = null;
    if (ct.includes("application/json")) {
      data = await res.json().catch(() => null);
    } else {
      data = await res.text().catch(() => "");
    }
    if (!res.ok) {
      const msg = (data && data.error) || (typeof data === "string" ? data : "") || res.statusText;
      const err = new Error(msg || "请求失败");
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  }

  function el(tag, attrs, ...children) {
    const node = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (k === "className") node.className = v;
        else if (k === "text") node.textContent = v;
        else if (k === "html") node.innerHTML = v;
        else if (k.startsWith("on") && typeof v === "function") {
          // onclick -> click, onsubmit -> submit
          const type = k.slice(2).toLowerCase();
          node.addEventListener(type, v);
        } else if (v === false || v == null) continue;
        else if (v === true) node.setAttribute(k, "");
        else node.setAttribute(k, String(v));
      }
    }
    // Flatten carefully: arrays of nodes ok; never spread a single DOM node
    const flat = [];
    const push = (c) => {
      if (c == null || c === false) return;
      if (Array.isArray(c)) {
        c.forEach(push);
        return;
      }
      flat.push(c);
    };
    children.forEach(push);
    for (const c of flat) {
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    }
    return node;
  }

  function badge(status) {
    const s = String(status || "").toLowerCase();
    let cls = "badge-muted";
    let text = status || "—";
    if (s === "active" || s === "enabled" || s === "ok") {
      cls = "badge-active";
      text = s === "active" || s === "enabled" ? "启用" : text;
    } else if (s === "error" || s === "failed") {
      cls = "badge-error";
      text = s === "error" ? "错误" : text;
    } else if (s === "disabled") {
      cls = "badge-disabled";
      text = "禁用";
    } else if (s === "expired") {
      cls = "badge-warning";
      text = "已过期";
    } else if (s === "warning" || s === "pending") {
      cls = "badge-warning";
    }
    return el("span", { className: "badge " + cls, text });
  }

  function statusBadge(code) {
    if (!code) return el("span", { className: "muted", text: "—" });
    let cls = "badge-muted";
    if (code >= 200 && code < 300) cls = "badge-success";
    else if (code >= 400) cls = "badge-danger";
    else if (code >= 300) cls = "badge-warning";
    return el("span", { className: "badge " + cls, text: String(code) });
  }

  function setFlash(msg) {
    state.flash = msg;
    state.error = "";
  }

  function setError(msg) {
    state.error = msg;
    state.flash = "";
  }

  function clearMessages() {
    state.error = "";
    state.flash = "";
  }

  // ---------- data loaders ----------
  async function checkAuth() {
    try {
      await api("/dashboard");
      state.authed = true;
      return true;
    } catch (e) {
      state.authed = false;
      return false;
    }
  }

  async function loadDashboard() {
    state.dashboard = await api("/dashboard");
  }

  async function loadAccounts() {
    const data = await api("/accounts");
    state.accounts = data.accounts || data || [];
  }

  async function loadKeys() {
    const data = await api("/keys");
    state.keys = data.keys || data || [];
  }

  async function loadLogs() {
    const q = new URLSearchParams();
    const f = state.logFilters;
    if (f.from) q.set("from", f.from);
    if (f.to) q.set("to", f.to);
    if (f.account_id) q.set("account_id", f.account_id);
    if (f.model) q.set("model", f.model);
    if (f.status) q.set("status", f.status);
    if (f.protocol) q.set("protocol", f.protocol);
    q.set("limit", "100");
    const data = await api("/logs?" + q.toString());
    state.logs = data.logs || data.items || data.data || data || [];
  }

  async function loadSettings() {
    state.settings = await api("/settings");
  }

  function goPage(id) {
    if (state.page === id && !state.loading) {
      // 同页再点：强制刷新
      loadPage();
      return;
    }
    state.page = id;
    state.selectedLog = null;
    state.modal = null;
    clearMessages();
    // 先立刻渲染目标页骨架，再拉数据（避免“点了没反应”）
    render();
    loadPage();
  }

  async function loadPage() {
    const seq = ++loadSeq;
    const page = state.page;
    state.loading = true;
    render();
    try {
      switch (page) {
        case "dashboard":
          await loadDashboard();
          break;
        case "accounts":
          await loadAccounts();
          break;
        case "keys":
          await loadKeys();
          break;
        case "logs":
          await loadLogs();
          break;
        case "settings":
          await loadSettings();
          break;
      }
    } catch (e) {
      if (e.status === 401) {
        state.authed = false;
      } else {
        setError(e.message);
      }
    } finally {
      // 只有最新一次加载才更新 UI
      if (seq === loadSeq) {
        state.loading = false;
        render();
      }
    }
  }

  // ---------- actions ----------
  async function doLogin(password) {
    try {
      await api("/login", { method: "POST", body: JSON.stringify({ password }) });
      state.authed = true;
      state.page = "dashboard";
      clearMessages();
      await loadPage();
    } catch (e) {
      setError(e.message || "登录失败");
      render();
    }
  }

  async function importAccountsFile(file) {
    try {
      const text = await file.text();
      const data = await api("/accounts/import", {
        method: "POST",
        body: text,
        headers: { "Content-Type": "application/json" },
      });
      setFlash("导入完成：新增 " + (data.inserted ?? 0) + "，更新 " + (data.updated ?? 0));
      await loadAccounts();
    } catch (e) {
      setError(e.message || "导入失败");
    }
    render();
  }

  async function patchAccount(id, body) {
    try {
      await api("/accounts/" + encodeURIComponent(id), {
        method: "PATCH",
        body: JSON.stringify(body),
      });
      setFlash("账号已更新");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function refreshAccount(id) {
    try {
      await api("/accounts/" + encodeURIComponent(id) + "/refresh", { method: "POST" });
      setFlash("Token 已刷新");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function deleteAccount(id) {
    if (!confirm("确定删除该账号？此操作不可恢复。")) return;
    try {
      await api("/accounts/" + encodeURIComponent(id), { method: "DELETE" });
      setFlash("账号已删除");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function exportAccount(id) {
    try {
      const data = await api("/accounts/" + encodeURIComponent(id) + "/export");
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = "xai-account-" + shortId(id) + ".json";
      a.click();
      URL.revokeObjectURL(a.href);
      setFlash("已导出 JSON");
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function startOAuth() {
    try {
      const data = await api("/accounts/oauth/start", { method: "POST", body: "{}" });
      state.modal = { type: "oauth", data, status: "等待授权…" };
      render();
      pollOAuth(data);
    } catch (e) {
      setError(e.message || "启动 OAuth 失败");
      render();
    }
  }

  async function pollOAuth(start) {
    const body = {
      device_code: start.device_code,
      token_endpoint: start.token_endpoint,
      enable: true,
    };
    const max = 60;
    for (let i = 0; i < max; i++) {
      if (!state.modal || state.modal.type !== "oauth") return;
      await sleep(2000);
      try {
        const res = await api("/accounts/oauth/poll", {
          method: "POST",
          body: JSON.stringify(body),
        });
        if (res && (res.account || res.id || res.status === "ok" || res.done)) {
          state.modal = null;
          setFlash("OAuth 登录成功，账号已写入");
          state.page = "accounts";
          await loadAccounts();
          render();
          return;
        }
        if (state.modal) {
          state.modal.status = res.status || res.message || "等待授权…";
          render();
        }
      } catch (e) {
        // authorization_pending 等继续等
        if (e.status && e.status !== 400 && e.status !== 428) {
          if (String(e.message || "").includes("pending") || String(e.message || "").includes("slow_down")) {
            if (state.modal) {
              state.modal.status = "等待授权…";
              render();
            }
            continue;
          }
          setError(e.message);
          state.modal = null;
          render();
          return;
        }
        if (state.modal) {
          state.modal.status = e.message || "等待授权…";
          render();
        }
      }
    }
    setError("OAuth 等待超时");
    state.modal = null;
    render();
  }

  async function createKey(label) {
    try {
      const data = await api("/keys", {
        method: "POST",
        body: JSON.stringify({ label: label || "" }),
      });
      state.modal = { type: "key-created", data };
      await loadKeys();
    } catch (e) {
      setError(e.message);
      state.modal = null;
    }
    render();
  }

  async function revokeKey(id) {
    if (!confirm("确定吊销该 API Key？")) return;
    try {
      await api("/keys/" + encodeURIComponent(id), { method: "DELETE" });
      setFlash("API Key 已吊销");
      await loadKeys();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function openLog(id) {
    try {
      const data = await api("/logs/" + encodeURIComponent(id));
      state.selectedLog = data.log || data;
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function saveSettings(payload) {
    try {
      state.settings = await api("/settings", {
        method: "PUT",
        body: JSON.stringify(payload),
      });
      setFlash("设置已保存");
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  function sleep(ms) {
    return new Promise((r) => setTimeout(r, ms));
  }

  // ---------- views ----------
  function renderLogin() {
    const form = el(
      "form",
      {
        className: "login-card",
        onsubmit: (e) => {
          e.preventDefault();
          const pw = e.target.password.value;
          doLogin(pw);
        },
      },
      el("h1", { text: "Grok Bridge" }),
      el("p", { className: "subtitle", text: "管理后台" }),
      state.error ? el("div", { className: "error-banner", text: state.error }) : null,
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "password", text: "管理员密码" }),
        el("input", {
          type: "password",
          id: "password",
          name: "password",
          autocomplete: "current-password",
          required: true,
          autofocus: true,
        })
      ),
      el("button", { type: "submit", className: "btn btn-primary btn-block", text: "登录" })
    );
    app.replaceChildren(el("div", { className: "login-page" }, form));
  }

  function navItem(id, label) {
    return el("button", {
      type: "button",
      className: "nav-item" + (state.page === id ? " active" : ""),
      text: label,
      onclick: (e) => {
        e.preventDefault();
        e.stopPropagation();
        goPage(id);
      },
    });
  }

  function renderShell(content) {
    const sidebar = el(
      "aside",
      { className: "sidebar" },
      el("div", { className: "brand", html: 'Grok <span>Bridge</span>' }),
      navItem("dashboard", "仪表盘"),
      navItem("accounts", "账号"),
      navItem("keys", "API 密钥"),
      navItem("logs", "请求日志"),
      navItem("settings", "设置"),
      el(
        "div",
        { className: "sidebar-footer" },
        el("button", {
          type: "button",
          className: "btn btn-ghost btn-sm btn-block",
          text: state.loading ? "加载中…" : "刷新本页",
          disabled: state.loading,
          onclick: (e) => {
            e.preventDefault();
            loadPage();
          },
        })
      )
    );

    const mainKids = [];
    if (state.loading) {
      mainKids.push(el("div", { className: "loading-bar", text: "加载中…" }));
    }
    if (state.error) mainKids.push(el("div", { className: "error-banner", text: state.error }));
    if (state.flash) mainKids.push(el("div", { className: "success-banner", text: state.flash }));
    mainKids.push(content);

    const shell = el("div", { className: "shell" }, sidebar, el("main", { className: "main" }, ...mainKids));

    const extras = [];
    if (state.modal) extras.push(renderModal());
    if (state.selectedLog) extras.push(renderLogDrawer());

    app.replaceChildren(shell, ...extras);
  }

  function renderDashboard() {
    const d = state.dashboard || {};
    const cards = el(
      "div",
      { className: "cards" },
      card("今日请求", d.today_count ?? "—"),
      card("今日错误", d.today_errors ?? "—", d.today_errors > 0),
      card("近 7 日请求", d.last_7d_count ?? "—"),
      card("近 7 日错误", d.last_7d_errors ?? "—", d.last_7d_errors > 0),
      card("可用账号", d.active_accounts ?? "—")
    );

    const topModels = namedList(d.top_models || []);
    const topAccounts = namedList(d.top_accounts || []);

    const content = el(
      "div",
      null,
      el("div", { className: "page-header" }, el("h2", { text: "仪表盘" })),
      cards,
      el(
        "div",
        { className: "two-col" },
        el("div", { className: "panel" }, el("h3", { text: "热门模型" }), topModels),
        el("div", { className: "panel" }, el("h3", { text: "热门账号" }), topAccounts)
      )
    );
    renderShell(content);
  }

  function card(label, value, danger) {
    return el(
      "div",
      { className: "card" },
      el("div", { className: "label", text: label }),
      el("div", { className: "value" + (danger ? " danger" : ""), text: String(value) })
    );
  }

  function namedList(items) {
    if (!items.length) return el("div", { className: "empty", text: "暂无数据" });
    return el(
      "ul",
      { className: "list-plain" },
      ...items.map((it) =>
        el(
          "li",
          null,
          el("span", { text: it.name || it.Name || "—" }),
          el("span", { className: "mono", text: String(it.count ?? it.Count ?? 0) })
        )
      )
    );
  }

  function renderAccounts() {
    const rows =
      state.accounts.length === 0
        ? [el("tr", null, el("td", { colspan: "8", className: "empty", text: "暂无账号。请导入 JSON 或使用 OAuth 登录。" }))]
        : state.accounts.map((a) =>
            el(
              "tr",
              null,
              el("td", null, el("div", { text: a.label || a.email || "—" }), el("div", { className: "mono muted", text: shortId(a.id) })),
              el("td", { text: a.email || "—" }),
              el("td", null, badge(a.status)),
              el("td", null,
                el("input", {
                  type: "number",
                  className: "weight-input",
                  min: "1",
                  max: "1000",
                  value: String(a.weight > 0 ? a.weight : 1),
                  title: "轮询权重（加权模式）",
                  onchange: (e) => {
                    const w = Number(e.target.value) || 1;
                    patchAccount(a.id, { weight: w });
                  },
                })
              ),
              el("td", { className: "mono muted", text: a.expires_at ? fmtTime(a.expires_at) : "—" }),
              el("td", { className: "mono muted", text: a.access_token_suffix || "—" }),
              el("td", { text: a.has_refresh_token ? "有" : "无" }),
              el(
                "td",
                null,
                el(
                  "div",
                  { className: "row-actions" },
                  a.status === "disabled"
                    ? el("button", {
                        type: "button",
                        className: "btn btn-sm",
                        text: "启用",
                        onclick: () => patchAccount(a.id, { status: "active" }),
                      })
                    : el("button", {
                        type: "button",
                        className: "btn btn-sm",
                        text: "禁用",
                        onclick: () => patchAccount(a.id, { status: "disabled" }),
                      }),
                  el("button", {
                    type: "button",
                    className: "btn btn-sm",
                    text: "刷新 Token",
                    onclick: () => refreshAccount(a.id),
                  }),
                  el("button", {
                    type: "button",
                    className: "btn btn-sm",
                    text: "导出",
                    onclick: () => exportAccount(a.id),
                  }),
                  el("button", {
                    type: "button",
                    className: "btn btn-sm btn-danger",
                    text: "删除",
                    onclick: () => deleteAccount(a.id),
                  })
                )
              )
            )
          );

    const fileInput = el("input", {
      type: "file",
      accept: ".json,application/json",
      className: "hidden",
      id: "import-file",
      onchange: (e) => {
        const f = e.target.files && e.target.files[0];
        if (f) importAccountsFile(f);
        e.target.value = "";
      },
    });

    const content = el(
      "div",
      null,
      el(
        "div",
        { className: "page-header" },
        el("h2", { text: "账号" }),
        el(
          "div",
          { className: "actions" },
          fileInput,
          el("button", {
            type: "button",
            className: "btn",
            text: "导入 JSON",
            onclick: () => {
              const input = document.getElementById("import-file");
              if (input) input.click();
            },
          }),
          el("button", {
            type: "button",
            className: "btn btn-primary",
            text: "OAuth 登录",
            onclick: () => startOAuth(),
          })
        )
      ),
      el(
        "div",
        { className: "table-wrap" },
        el(
          "table",
          null,
          el(
            "thead",
            null,
            el(
              "tr",
              null,
              el("th", { text: "账号" }),
              el("th", { text: "邮箱" }),
              el("th", { text: "状态" }),
              el("th", { text: "权重" }),
              el("th", { text: "过期时间" }),
              el("th", { text: "Token" }),
              el("th", { text: "Refresh" }),
              el("th", { text: "操作" })
            )
          ),
          el("tbody", null, ...rows)
        )
      )
    );
    renderShell(content);
  }

  function renderKeys() {
    const rows =
      state.keys.length === 0
        ? [el("tr", null, el("td", { colspan: "5", className: "empty", text: "暂无 API 密钥。" }))]
        : state.keys.map((k) =>
            el(
              "tr",
              null,
              el("td", { text: k.label || "—" }),
              el("td", { className: "mono", text: k.key_prefix || shortId(k.id) }),
              el("td", null, badge(k.enabled ? "active" : "disabled")),
              el("td", { className: "muted", text: k.last_used_at ? fmtTime(k.last_used_at) : "从未使用" }),
              el(
                "td",
                null,
                el("button", {
                  type: "button",
                  className: "btn btn-sm btn-danger",
                  text: "吊销",
                  onclick: () => revokeKey(k.id),
                })
              )
            )
          );

    const content = el(
      "div",
      null,
      el(
        "div",
        { className: "page-header" },
        el("h2", { text: "API 密钥" }),
        el(
          "div",
          { className: "actions" },
          el("button", {
            type: "button",
            className: "btn btn-primary",
            text: "创建密钥",
            onclick: () => {
              state.modal = { type: "create-key" };
              render();
            },
          })
        )
      ),
      el(
        "div",
        { className: "table-wrap" },
        el(
          "table",
          null,
          el(
            "thead",
            null,
            el(
              "tr",
              null,
              el("th", { text: "备注" }),
              el("th", { text: "前缀" }),
              el("th", { text: "状态" }),
              el("th", { text: "最近使用" }),
              el("th", { text: "操作" })
            )
          ),
          el("tbody", null, ...rows)
        )
      )
    );
    renderShell(content);
  }

  function renderLogs() {
    const f = state.logFilters;
    const filters = el(
      "div",
      { className: "filters" },
      field(
        "开始时间",
        el("input", {
          type: "text",
          placeholder: "RFC3339",
          value: f.from,
          oninput: (e) => {
            f.from = e.target.value;
          },
        })
      ),
      field(
        "结束时间",
        el("input", {
          type: "text",
          placeholder: "RFC3339",
          value: f.to,
          oninput: (e) => {
            f.to = e.target.value;
          },
        })
      ),
      field(
        "账号 ID",
        el("input", {
          type: "text",
          value: f.account_id,
          oninput: (e) => {
            f.account_id = e.target.value;
          },
        })
      ),
      field(
        "模型",
        el("input", {
          type: "text",
          value: f.model,
          oninput: (e) => {
            f.model = e.target.value;
          },
        })
      ),
      field(
        "状态码",
        el("input", {
          type: "text",
          placeholder: "例如 500",
          value: f.status,
          oninput: (e) => {
            f.status = e.target.value;
          },
        })
      ),
      field(
        "协议",
        el(
          "select",
          {
            onchange: (e) => {
              f.protocol = e.target.value;
            },
          },
          el("option", { value: "", text: "全部", selected: !f.protocol }),
          el("option", { value: "claude", text: "claude", selected: f.protocol === "claude" }),
          el("option", { value: "openai_chat", text: "openai_chat", selected: f.protocol === "openai_chat" }),
          el("option", { value: "openai_responses", text: "openai_responses", selected: f.protocol === "openai_responses" })
        )
      ),
      el("button", {
        type: "button",
        className: "btn btn-primary",
        text: "查询",
        onclick: () => loadPage(),
      })
    );

    const rows =
      state.logs.length === 0
        ? [el("tr", null, el("td", { colspan: "8", className: "empty", text: "没有匹配的日志。" }))]
        : state.logs.map((log) =>
            el(
              "tr",
              {
                className: "clickable",
                onclick: () => openLog(log.id),
              },
              el("td", { className: "muted mono", text: fmtTime(log.created_at) }),
              el("td", null, statusBadge(log.status_code)),
              el("td", { text: log.protocol || "—" }),
              el("td", { className: "mono", text: log.model_requested || "—" }),
              el("td", { text: log.account_label || shortId(log.account_id) || "—" }),
              el("td", { text: log.api_key_label || shortId(log.api_key_id) || "—" }),
              el("td", { className: "mono", text: log.latency_ms != null ? log.latency_ms + "ms" : "—" }),
              el("td", { className: "muted", text: log.error_message ? truncate(log.error_message, 40) : "" })
            )
          );

    const content = el(
      "div",
      null,
      el("div", { className: "page-header" }, el("h2", { text: "请求日志" })),
      filters,
      el(
        "div",
        { className: "table-wrap" },
        el(
          "table",
          null,
          el(
            "thead",
            null,
            el(
              "tr",
              null,
              el("th", { text: "时间" }),
              el("th", { text: "状态" }),
              el("th", { text: "协议" }),
              el("th", { text: "模型" }),
              el("th", { text: "账号" }),
              el("th", { text: "API 密钥" }),
              el("th", { text: "耗时" }),
              el("th", { text: "错误" })
            )
          ),
          el("tbody", null, ...rows)
        )
      )
    );
    renderShell(content);
  }

  function field(label, input) {
    return el("div", { className: "form-group" }, el("label", { text: label }), input);
  }

  function renderSettings() {
    const s = state.settings || {
      log_bodies: "errors_only",
      retention: 30,
      http_proxy: "",
      scheduling: "round_robin",
      max_concurrency: 0,
      account_concurrency: 0,
      max_account_switches: 2,
      max_transient_retries: 2,
    };
    let logBodies = s.log_bodies || "errors_only";
    let retention = s.retention ?? s.log_retention_days ?? 30;
    let scheduling = s.scheduling || "round_robin";

    const form = el(
      "form",
      {
        className: "panel settings-panel",
        onsubmit: (e) => {
          e.preventDefault();
          const payload = {
            log_bodies: e.target.log_bodies.value,
            retention: Number(e.target.retention.value),
            http_proxy: e.target.http_proxy.value.trim(),
            scheduling: e.target.scheduling.value,
            max_concurrency: Number(e.target.max_concurrency.value) || 0,
            account_concurrency: Number(e.target.account_concurrency.value) || 0,
            max_account_switches: Number(e.target.max_account_switches.value) || 0,
            max_transient_retries: Number(e.target.max_transient_retries.value) || 0,
          };
          const pw = (e.target.admin_password.value || "").trim();
          if (pw) payload.admin_password = pw;
          saveSettings(payload).then(() => {
            e.target.admin_password.value = "";
          });
        },
      },
      el("h3", { className: "settings-section", text: "上游网络" }),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "http_proxy", text: "HTTP/SOCKS 代理" }),
        el("input", {
          type: "text",
          id: "http_proxy",
          name: "http_proxy",
          placeholder: "例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080，留空用环境变量",
          value: s.http_proxy || "",
        }),
        el("p", { className: "muted", text: "仅影响访问 xAI / OAuth 的出站请求。保存后立即生效。" })
      ),
      el("h3", { className: "settings-section", text: "账号调度" }),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "scheduling", text: "轮询策略" }),
        el(
          "select",
          { id: "scheduling", name: "scheduling" },
          el("option", { value: "round_robin", text: "轮询（round_robin）", selected: scheduling === "round_robin" }),
          el("option", { value: "weighted", text: "加权轮询（weighted）", selected: scheduling === "weighted" })
        ),
        el("p", { className: "muted", text: "加权模式按账号「权重」分配流量，可在账号列表中修改权重。" })
      ),
      el(
        "div",
        { className: "form-row" },
        el(
          "div",
          { className: "form-group" },
          el("label", { for: "max_account_switches", text: "失败切号次数" }),
          el("input", {
            type: "number",
            id: "max_account_switches",
            name: "max_account_switches",
            min: "0",
            value: String(s.max_account_switches ?? 2),
          })
        ),
        el(
          "div",
          { className: "form-group" },
          el("label", { for: "max_transient_retries", text: "瞬时重试（预留）" }),
          el("input", {
            type: "number",
            id: "max_transient_retries",
            name: "max_transient_retries",
            min: "0",
            value: String(s.max_transient_retries ?? 2),
          })
        )
      ),
      el("h3", { className: "settings-section", text: "并发限制" }),
      el(
        "div",
        { className: "form-row" },
        el(
          "div",
          { className: "form-group" },
          el("label", { for: "max_concurrency", text: "全局最大并发" }),
          el("input", {
            type: "number",
            id: "max_concurrency",
            name: "max_concurrency",
            min: "0",
            value: String(s.max_concurrency ?? 0),
          }),
          el("p", { className: "muted", text: "0 = 不限制" })
        ),
        el(
          "div",
          { className: "form-group" },
          el("label", { for: "account_concurrency", text: "单账号最大并发" }),
          el("input", {
            type: "number",
            id: "account_concurrency",
            name: "account_concurrency",
            min: "0",
            value: String(s.account_concurrency ?? 0),
          }),
          el("p", { className: "muted", text: "0 = 不限制" })
        )
      ),
      el("h3", { className: "settings-section", text: "日志" }),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "log_bodies", text: "请求体记录" }),
        el(
          "select",
          { id: "log_bodies", name: "log_bodies" },
          ...[
            ["off", "关闭"],
            ["errors_only", "仅错误"],
            ["sample", "采样"],
            ["all", "全部"],
          ].map(([v, lab]) => el("option", { value: v, text: lab, selected: logBodies === v }))
        )
      ),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "retention", text: "日志保留天数" }),
        el("input", {
          type: "number",
          id: "retention",
          name: "retention",
          min: "0",
          value: String(retention),
        })
      ),
      el("h3", { className: "settings-section", text: "安全" }),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "admin_password", text: "新管理员密码（可选）" }),
        el("input", {
          type: "password",
          id: "admin_password",
          name: "admin_password",
          autocomplete: "new-password",
          placeholder: "留空则不修改",
        }),
        el("p", {
          className: "muted",
          text: "立即更新当前进程。若未设置环境变量 GROK_BRIDGE_ADMIN_PASSWORD，会写入数据库以便重启后生效。",
        })
      ),
      el("button", { type: "submit", className: "btn btn-primary", text: "保存设置" })
    );

    const content = el("div", null, el("div", { className: "page-header" }, el("h2", { text: "设置" })), form);
    renderShell(content);
  }

  function renderModal() {
    const m = state.modal;
    if (!m) return null;

    if (m.type === "create-key") {
      return el(
        "div",
        {
          className: "modal-backdrop",
          onclick: (e) => {
            if (e.target === e.currentTarget) {
              state.modal = null;
              render();
            }
          },
        },
        el(
          "div",
          { className: "modal" },
          el("h3", { text: "创建 API 密钥" }),
          el(
            "form",
            {
              onsubmit: (e) => {
                e.preventDefault();
                createKey(e.target.label.value.trim());
              },
            },
            el(
              "div",
              { className: "form-group" },
              el("label", { for: "key-label", text: "备注" }),
              el("input", { id: "key-label", name: "label", placeholder: "例如 claude-code", autofocus: true })
            ),
            el(
              "div",
              { className: "modal-actions" },
              el("button", {
                type: "button",
                className: "btn btn-ghost",
                text: "取消",
                onclick: () => {
                  state.modal = null;
                  render();
                },
              }),
              el("button", { type: "submit", className: "btn btn-primary", text: "创建" })
            )
          )
        )
      );
    }

    if (m.type === "key-created") {
      const plain = m.data.key || m.data.plaintext || "";
      return el(
        "div",
        { className: "modal-backdrop" },
        el(
          "div",
          { className: "modal" },
          el("h3", { text: "API 密钥已创建" }),
          el("p", { className: "muted", text: "请立即复制。关闭后将无法再次查看明文。" }),
          el("div", { className: "plaintext-key", text: plain }),
          el(
            "div",
            { className: "modal-actions" },
            el("button", {
              type: "button",
              className: "btn",
              text: "复制",
              onclick: async () => {
                try {
                  await navigator.clipboard.writeText(plain);
                  setFlash("已复制到剪贴板");
                  render();
                } catch (_) {
                  /* ignore */
                }
              },
            }),
            el("button", {
              type: "button",
              className: "btn btn-primary",
              text: "完成",
              onclick: () => {
                state.modal = null;
                render();
              },
            })
          )
        )
      );
    }

    if (m.type === "oauth") {
      const d = m.data || {};
      const uri = d.verification_uri_complete || d.verification_uri || "";
      return el(
        "div",
        { className: "modal-backdrop" },
        el(
          "div",
          { className: "modal" },
          el("h3", { text: "OAuth 设备码登录" }),
          el("p", { className: "muted", text: "打开链接并输入代码，或直接打开完整验证链接。" }),
          el("div", { className: "oauth-code", text: d.user_code || "—" }),
          uri ? el("p", null, el("a", { href: uri, target: "_blank", rel: "noopener", text: uri })) : null,
          el("p", { className: "muted", text: "状态：" + (m.status || "等待授权…") }),
          el(
            "div",
            { className: "modal-actions" },
            el("button", {
              type: "button",
              className: "btn btn-ghost",
              text: "取消",
              onclick: () => {
                state.modal = null;
                render();
              },
            })
          )
        )
      );
    }

    return null;
  }

  function renderLogDrawer() {
    const log = state.selectedLog;
    if (!log) return null;

    const close = () => {
      state.selectedLog = null;
      render();
    };

    const kvPairs = [
      ["ID", log.id],
      ["Request ID", log.request_id],
      ["时间", log.created_at],
      ["状态码", log.status_code],
      ["协议", log.protocol],
      ["路径", log.path],
      ["请求模型", log.model_requested],
      ["上游模型", log.model_upstream],
      ["流式", log.stream ? "是" : "否"],
      ["账号", (log.account_label || "") + " " + (log.account_id || "")],
      ["API 密钥", (log.api_key_label || "") + " " + (log.api_key_id || "")],
      ["耗时", log.latency_ms != null ? log.latency_ms + " ms" : "—"],
      ["Token 入/出", (log.input_tokens ?? "—") + " / " + (log.output_tokens ?? "—")],
      ["客户端 IP", log.client_ip],
      ["User-Agent", log.user_agent],
      ["错误", log.error_code ? log.error_code + ": " + (log.error_message || "") : log.error_message || ""],
    ];

    const body = el(
      "div",
      { className: "drawer-body" },
      el(
        "dl",
        { className: "kv" },
        ...kvPairs.flatMap(([k, v]) => [el("dt", { text: k }), el("dd", { text: v == null || v === "" ? "—" : String(v) })])
      ),
      el("h4", { text: "请求体" }),
      el("pre", { className: "body-block", text: prettyJSON(log.request_body) || "（空）" }),
      el("h4", { text: "响应体" }),
      el("pre", { className: "body-block", text: prettyJSON(log.response_body) || "（空）" })
    );

    return el(
      "div",
      null,
      el("div", { className: "drawer-backdrop", onclick: close }),
      el(
        "div",
        { className: "drawer" },
        el(
          "div",
          { className: "drawer-header" },
          el("h3", { text: "日志详情" }),
          el("button", { type: "button", className: "btn btn-ghost btn-sm", text: "关闭", onclick: close })
        ),
        body
      )
    );
  }

  function prettyJSON(s) {
    if (!s) return "";
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch (_) {
      return String(s);
    }
  }

  function shortId(id) {
    if (!id) return "";
    return id.length > 12 ? id.slice(0, 8) + "…" : id;
  }

  function fmtTime(s) {
    if (!s) return "—";
    try {
      const d = new Date(s);
      if (isNaN(d.getTime())) return s;
      return d.toLocaleString();
    } catch (_) {
      return s;
    }
  }

  function truncate(s, n) {
    s = String(s);
    return s.length > n ? s.slice(0, n) + "…" : s;
  }

  function render() {
    if (!state.authed) {
      renderLogin();
      return;
    }
    switch (state.page) {
      case "accounts":
        renderAccounts();
        break;
      case "keys":
        renderKeys();
        break;
      case "logs":
        renderLogs();
        break;
      case "settings":
        renderSettings();
        break;
      default:
        renderDashboard();
    }
  }

  // ---------- boot ----------
  async function boot() {
    const ok = await checkAuth();
    if (ok) {
      await loadPage();
    } else {
      renderLogin();
    }
  }

  boot();
})();
