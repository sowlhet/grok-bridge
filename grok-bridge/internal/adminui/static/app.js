/* Grok Bridge Admin SPA — vanilla JS, no build step */
(function () {
  "use strict";

  const API = "/admin/api";
  const app = document.getElementById("app");

  const state = {
    authed: false,
    page: "dashboard",
    error: "",
    flash: "",
    dashboard: null,
    accounts: [],
    keys: [],
    logs: [],
    settings: null,
    logFilters: { from: "", to: "", account_id: "", model: "", status: "", protocol: "" },
    selectedLog: null,
    modal: null, // { type, data }
  };

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
      const err = new Error(msg || "request failed");
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
        else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2).toLowerCase(), v);
        else if (v === false || v == null) continue;
        else if (v === true) node.setAttribute(k, "");
        else node.setAttribute(k, v);
      }
    }
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    }
    return node;
  }

  function esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function badge(status) {
    const s = String(status || "").toLowerCase();
    let cls = "badge-muted";
    if (s === "active" || s === "enabled" || s === "ok") cls = "badge-active";
    else if (s === "error" || s === "failed") cls = "badge-error";
    else if (s === "disabled") cls = "badge-disabled";
    else if (s === "warning" || s === "pending") cls = "badge-warning";
    return el("span", { className: "badge " + cls, text: status || "—" });
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

  async function loadPage() {
    clearMessages();
    try {
      switch (state.page) {
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
    }
    render();
  }

  // ---------- actions ----------
  async function doLogin(password) {
    try {
      await api("/login", { method: "POST", body: JSON.stringify({ password }) });
      state.authed = true;
      state.page = "dashboard";
      await loadPage();
    } catch (e) {
      setError(e.message || "Login failed");
      render();
    }
  }

  async function importAccountsFile(file) {
    try {
      const fd = new FormData();
      fd.append("file", file);
      const data = await api("/accounts/import", { method: "POST", body: fd });
      setFlash(`Imported: inserted ${data.inserted ?? 0}, updated ${data.updated ?? 0}`);
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    state.modal = null;
    render();
  }

  async function patchAccount(id, body) {
    try {
      await api("/accounts/" + encodeURIComponent(id), {
        method: "PATCH",
        body: JSON.stringify(body),
      });
      setFlash("Account updated");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function deleteAccount(id) {
    if (!confirm("Delete this account?")) return;
    try {
      await api("/accounts/" + encodeURIComponent(id), { method: "DELETE" });
      setFlash("Account deleted");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function refreshAccount(id) {
    try {
      await api("/accounts/" + encodeURIComponent(id) + "/refresh", { method: "POST" });
      setFlash("Token refreshed");
      await loadAccounts();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  function exportAccount(id) {
    // Use cookie credentials via same-origin navigation
    window.open(API + "/accounts/" + encodeURIComponent(id) + "/export", "_blank");
  }

  async function startOAuth() {
    try {
      const data = await api("/accounts/oauth/start", { method: "POST", body: "{}" });
      state.modal = { type: "oauth", data, polling: false, status: "waiting" };
      render();
      pollOAuth(data);
    } catch (e) {
      setError(e.message);
      render();
    }
  }

  async function pollOAuth(dc) {
    if (!state.modal || state.modal.type !== "oauth") return;
    state.modal.polling = true;
    const interval = Math.max(2, Number(dc.interval) || 5) * 1000;
    const deadline = Date.now() + (Number(dc.expires_in) || 600) * 1000;

    while (state.modal && state.modal.type === "oauth" && Date.now() < deadline) {
      await sleep(interval);
      if (!state.modal || state.modal.type !== "oauth") return;
      try {
        const res = await api("/accounts/oauth/poll", {
          method: "POST",
          body: JSON.stringify({
            device_code: dc.device_code,
            token_endpoint: dc.token_endpoint || "",
            enable: true,
          }),
        });
        if (res.status === "pending") {
          state.modal.status = "pending";
          render();
          continue;
        }
        if (res.status === "authorized" || res.account) {
          state.modal = null;
          setFlash("Account authorized: " + (res.account?.email || res.account?.label || "ok"));
          state.page = "accounts";
          await loadAccounts();
          render();
          return;
        }
      } catch (e) {
        state.modal.status = "error: " + e.message;
        render();
        // keep polling on transient errors unless expired
      }
    }
    if (state.modal && state.modal.type === "oauth") {
      state.modal.status = "expired";
      render();
    }
  }

  function sleep(ms) {
    return new Promise((r) => setTimeout(r, ms));
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
    if (!confirm("Revoke this API key?")) return;
    try {
      await api("/keys/" + encodeURIComponent(id), { method: "DELETE" });
      setFlash("Key revoked");
      await loadKeys();
    } catch (e) {
      setError(e.message);
    }
    render();
  }

  async function openLog(id) {
    try {
      state.selectedLog = await api("/logs/" + encodeURIComponent(id));
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
      setFlash("Settings saved");
    } catch (e) {
      setError(e.message);
    }
    render();
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
      el("p", { className: "subtitle", text: "Admin console" }),
      state.error ? el("div", { className: "error-banner", text: state.error }) : null,
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "password", text: "Password" }),
        el("input", {
          type: "password",
          id: "password",
          name: "password",
          autocomplete: "current-password",
          required: true,
          autofocus: true,
        })
      ),
      el("button", { type: "submit", className: "btn btn-primary btn-block", text: "Sign in" })
    );
    app.replaceChildren(el("div", { className: "login-page" }, form));
  }

  function navItem(id, label) {
    return el("button", {
      type: "button",
      className: "nav-item" + (state.page === id ? " active" : ""),
      text: label,
      onclick: () => {
        state.page = id;
        state.selectedLog = null;
        state.modal = null;
        loadPage();
      },
    });
  }

  function renderShell(content) {
    const sidebar = el(
      "aside",
      { className: "sidebar" },
      el("div", { className: "brand", html: 'Grok <span>Bridge</span>' }),
      navItem("dashboard", "Dashboard"),
      navItem("accounts", "Accounts"),
      navItem("keys", "API Keys"),
      navItem("logs", "Logs"),
      navItem("settings", "Settings"),
      el(
        "div",
        { className: "sidebar-footer" },
        el("button", {
          type: "button",
          className: "btn btn-ghost btn-sm btn-block",
          text: "Refresh",
          onclick: () => loadPage(),
        })
      )
    );

    const mainKids = [];
    if (state.error) mainKids.push(el("div", { className: "error-banner", text: state.error }));
    if (state.flash) mainKids.push(el("div", { className: "success-banner", text: state.flash }));
    mainKids.push(content);

    const shell = el(
      "div",
      { className: "shell" },
      sidebar,
      el("main", { className: "main" }, ...mainKids)
    );

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
      card("Today", d.today_count ?? "—"),
      card("Today errors", d.today_errors ?? "—", d.today_errors > 0),
      card("Last 7 days", d.last_7d_count ?? "—"),
      card("7d errors", d.last_7d_errors ?? "—", d.last_7d_errors > 0),
      card("Active accounts", d.active_accounts ?? "—")
    );

    const topModels = namedList(d.top_models || []);
    const topAccounts = namedList(d.top_accounts || []);

    const content = el(
      "div",
      null,
      el("div", { className: "page-header" }, el("h2", { text: "Dashboard" })),
      cards,
      el(
        "div",
        { className: "two-col" },
        el("div", { className: "panel" }, el("h3", { text: "Top models" }), topModels),
        el("div", { className: "panel" }, el("h3", { text: "Top accounts" }), topAccounts)
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
    if (!items.length) return el("div", { className: "empty", text: "No data yet" });
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
        ? el("tr", null, el("td", { colspan: "7", className: "empty", text: "No accounts yet. Import JSON or start OAuth." }))
        : state.accounts.map((a) =>
            el(
              "tr",
              null,
              el("td", null, el("div", { text: a.label || a.email || "—" }), el("div", { className: "mono muted", text: shortId(a.id) })),
              el("td", { text: a.email || "—" }),
              el("td", null, badge(a.status)),
              el("td", { className: "mono muted", text: a.expires_at ? fmtTime(a.expires_at) : "—" }),
              el("td", { className: "mono muted", text: a.access_token_suffix || "—" }),
              el("td", { text: a.has_refresh_token ? "yes" : "no" }),
              el(
                "td",
                null,
                el(
                  "div",
                  { className: "row-actions" },
                  a.status === "disabled"
                    ? el("button", {
                        className: "btn btn-sm",
                        text: "Enable",
                        onclick: () => patchAccount(a.id, { status: "active" }),
                      })
                    : el("button", {
                        className: "btn btn-sm",
                        text: "Disable",
                        onclick: () => patchAccount(a.id, { status: "disabled" }),
                      }),
                  el("button", {
                    className: "btn btn-sm",
                    text: "Refresh",
                    onclick: () => refreshAccount(a.id),
                  }),
                  el("button", {
                    className: "btn btn-sm",
                    text: "Export",
                    onclick: () => exportAccount(a.id),
                  }),
                  el("button", {
                    className: "btn btn-sm btn-danger",
                    text: "Delete",
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
        el("h2", { text: "Accounts" }),
        el(
          "div",
          { className: "actions" },
          fileInput,
          el("button", {
            className: "btn",
            text: "Import JSON",
            onclick: () => document.getElementById("import-file").click(),
          }),
          el("button", {
            className: "btn btn-primary",
            text: "OAuth login",
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
              el("th", { text: "Account" }),
              el("th", { text: "Email" }),
              el("th", { text: "Status" }),
              el("th", { text: "Expires" }),
              el("th", { text: "Token" }),
              el("th", { text: "Refresh" }),
              el("th", { text: "Actions" })
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
        ? el("tr", null, el("td", { colspan: "5", className: "empty", text: "No API keys yet." }))
        : state.keys.map((k) =>
            el(
              "tr",
              null,
              el("td", { text: k.label || "—" }),
              el("td", { className: "mono", text: k.key_prefix || shortId(k.id) }),
              el("td", null, badge(k.enabled ? "active" : "disabled")),
              el("td", { className: "muted", text: k.last_used_at ? fmtTime(k.last_used_at) : "never" }),
              el(
                "td",
                null,
                el("button", {
                  className: "btn btn-sm btn-danger",
                  text: "Revoke",
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
        el("h2", { text: "API Keys" }),
        el(
          "div",
          { className: "actions" },
          el("button", {
            className: "btn btn-primary",
            text: "Create key",
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
              el("th", { text: "Label" }),
              el("th", { text: "Prefix" }),
              el("th", { text: "Status" }),
              el("th", { text: "Last used" }),
              el("th", { text: "Actions" })
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
      field("From", el("input", {
        type: "text",
        placeholder: "RFC3339",
        value: f.from,
        oninput: (e) => { f.from = e.target.value; },
      })),
      field("To", el("input", {
        type: "text",
        placeholder: "RFC3339",
        value: f.to,
        oninput: (e) => { f.to = e.target.value; },
      })),
      field("Account ID", el("input", {
        type: "text",
        value: f.account_id,
        oninput: (e) => { f.account_id = e.target.value; },
      })),
      field("Model", el("input", {
        type: "text",
        value: f.model,
        oninput: (e) => { f.model = e.target.value; },
      })),
      field("Status", el("input", {
        type: "text",
        placeholder: "e.g. 500",
        value: f.status,
        oninput: (e) => { f.status = e.target.value; },
      })),
      field("Protocol", el("select", {
        onchange: (e) => { f.protocol = e.target.value; },
      },
        el("option", { value: "", text: "Any", selected: !f.protocol }),
        el("option", { value: "claude", text: "claude", selected: f.protocol === "claude" }),
        el("option", { value: "openai_chat", text: "openai_chat", selected: f.protocol === "openai_chat" }),
        el("option", { value: "openai_responses", text: "openai_responses", selected: f.protocol === "openai_responses" })
      )),
      el("button", {
        className: "btn btn-primary",
        text: "Apply",
        onclick: () => loadPage(),
      })
    );

    const rows =
      state.logs.length === 0
        ? el("tr", null, el("td", { colspan: "8", className: "empty", text: "No logs match." }))
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
      el("div", { className: "page-header" }, el("h2", { text: "Request Logs" })),
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
              el("th", { text: "Time" }),
              el("th", { text: "Status" }),
              el("th", { text: "Protocol" }),
              el("th", { text: "Model" }),
              el("th", { text: "Account" }),
              el("th", { text: "API key" }),
              el("th", { text: "Latency" }),
              el("th", { text: "Error" })
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
    const s = state.settings || { log_bodies: "errors_only", retention: 30 };
    let logBodies = s.log_bodies || "errors_only";
    let retention = s.retention ?? s.log_retention_days ?? 30;

    const form = el(
      "form",
      {
        className: "panel",
        style: "max-width:480px",
        onsubmit: (e) => {
          e.preventDefault();
          const payload = {
            log_bodies: e.target.log_bodies.value,
            retention: Number(e.target.retention.value),
          };
          const pw = (e.target.admin_password.value || "").trim();
          if (pw) {
            payload.admin_password = pw;
          }
          saveSettings(payload).then(() => {
            e.target.admin_password.value = "";
          });
        },
      },
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "log_bodies", text: "Log bodies" }),
        el(
          "select",
          { id: "log_bodies", name: "log_bodies" },
          ...["off", "errors_only", "sample", "all"].map((v) =>
            el("option", { value: v, text: v, selected: logBodies === v })
          )
        )
      ),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "retention", text: "Retention (days)" }),
        el("input", {
          type: "number",
          id: "retention",
          name: "retention",
          min: "0",
          value: String(retention),
        })
      ),
      el(
        "div",
        { className: "form-group" },
        el("label", { for: "admin_password", text: "New admin password (optional)" }),
        el("input", {
          type: "password",
          id: "admin_password",
          name: "admin_password",
          autocomplete: "new-password",
          placeholder: "Leave blank to keep current",
        }),
        el("p", {
          className: "muted",
          text: "Updates the running server. If GROK_BRIDGE_ADMIN_PASSWORD is unset, also persists to the settings table across restarts. Prefer env for production secrets.",
        })
      ),
      el("button", { type: "submit", className: "btn btn-primary", text: "Save settings" })
    );

    const content = el(
      "div",
      null,
      el("div", { className: "page-header" }, el("h2", { text: "Settings" })),
      form
    );
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
          el("h3", { text: "Create API key" }),
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
              el("label", { for: "key-label", text: "Label" }),
              el("input", { id: "key-label", name: "label", placeholder: "e.g. claude-code", autofocus: true })
            ),
            el(
              "div",
              { className: "modal-actions" },
              el("button", {
                type: "button",
                className: "btn btn-ghost",
                text: "Cancel",
                onclick: () => {
                  state.modal = null;
                  render();
                },
              }),
              el("button", { type: "submit", className: "btn btn-primary", text: "Create" })
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
          el("h3", { text: "API key created" }),
          el("p", { className: "muted", text: "Copy this key now. It will not be shown again." }),
          el("div", { className: "plaintext-key", text: plain }),
          el(
            "div",
            { className: "modal-actions" },
            el("button", {
              className: "btn",
              text: "Copy",
              onclick: async () => {
                try {
                  await navigator.clipboard.writeText(plain);
                  setFlash("Copied to clipboard");
                } catch (_) {
                  /* ignore */
                }
              },
            }),
            el("button", {
              className: "btn btn-primary",
              text: "Done",
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
          el("h3", { text: "OAuth device login" }),
          el("p", { className: "muted", text: "Open the link and enter the code, or use the complete URL." }),
          el("div", { className: "oauth-code", text: d.user_code || "—" }),
          uri
            ? el("p", null, el("a", { href: uri, target: "_blank", rel: "noopener", text: uri }))
            : null,
          el("p", { className: "muted", text: "Status: " + (m.status || "waiting") }),
          el(
            "div",
            { className: "modal-actions" },
            el("button", {
              className: "btn btn-ghost",
              text: "Cancel",
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
      ["Time", log.created_at],
      ["Status", log.status_code],
      ["Protocol", log.protocol],
      ["Path", log.path],
      ["Model requested", log.model_requested],
      ["Model upstream", log.model_upstream],
      ["Stream", log.stream ? "yes" : "no"],
      ["Account", (log.account_label || "") + " " + (log.account_id || "")],
      ["API key", (log.api_key_label || "") + " " + (log.api_key_id || "")],
      ["Latency", log.latency_ms != null ? log.latency_ms + " ms" : "—"],
      ["Tokens in/out", (log.input_tokens ?? "—") + " / " + (log.output_tokens ?? "—")],
      ["Client IP", log.client_ip],
      ["User agent", log.user_agent],
      ["Error", log.error_code ? log.error_code + ": " + (log.error_message || "") : log.error_message || ""],
    ];

    const body = el(
      "div",
      { className: "drawer-body" },
      el(
        "dl",
        { className: "kv" },
        ...kvPairs.flatMap(([k, v]) => [el("dt", { text: k }), el("dd", { text: v == null || v === "" ? "—" : String(v) })])
      ),
      el("h4", { text: "Request body" }),
      el("pre", { className: "body-block", text: prettyJSON(log.request_body) || "(empty)" }),
      el("h4", { text: "Response body" }),
      el("pre", { className: "body-block", text: prettyJSON(log.response_body) || "(empty)" })
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
          el("h3", { text: "Log detail" }),
          el("button", { className: "btn btn-ghost btn-sm", text: "Close", onclick: close })
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
