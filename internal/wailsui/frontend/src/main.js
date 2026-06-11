const state = {
  config: {
    provider: "krill",
    email: "",
    newapiBaseUrl: "",
    newapiAutoCallback: true,
    rememberLogin: true,
    refreshSec: 60,
    onTop: true,
    glassEnabled: true,
  },
  snapshot: {
    spend: 0,
    wallet: 0,
    req: "-",
    cache: "-",
    summary: {},
    subscriptions: [],
    timeLabel: "",
    err: "正在检查登录状态...",
    ok: false,
    loggedIn: false,
  },
  modal: "",
  busy: false,
  formError: "",
  oauthMessage: "",
  oauthAuthorizeUrl: "",
};

const app = document.querySelector("#app");

function backend() {
  const root = window.go || {};
  if (root.wailsui?.App) {
    return root.wailsui.App;
  }
  for (const pkg of Object.values(root)) {
    if (pkg?.App?.Bootstrap) {
      return pkg.App;
    }
  }
  return null;
}

async function waitForBackend() {
  for (let i = 0; i < 200; i += 1) {
    const api = backend();
    if (api) {
      return api;
    }
    await delay(25);
  }
  throw new Error("Wails backend is not ready");
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function money(value, digits = 2) {
  const n = Number(value || 0);
  return `$${n.toLocaleString("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
}

function plainNumber(value, digits = 0) {
  const n = Number(value || 0);
  return `$${n.toLocaleString("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
}

function text(value, fallback = "") {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  return String(value);
}

function escapeHTML(value) {
  return text(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function pct(value) {
  return Math.max(0, Math.min(100, Number(value || 0)));
}

function statCards(snapshot) {
  const summary = snapshot.summary || {};
  const totalDaily = Number(summary.totalDailyQuotaUsd || 0);
  const forwardedRemaining = Number(summary.totalForwardedRemainingUsd || 0);
  return [
    {
      title: "今日花费",
      value: money(snapshot.spend, 2),
      sub: `转结 ${money(forwardedRemaining, 2)} · 剩余 ${money(Math.max(0, totalDaily - Number(snapshot.spend || 0)), 2)}`,
      color: "#ffad2f",
    },
    {
      title: "钱包余额",
      value: money(snapshot.wallet, 2),
      sub: Number(snapshot.wallet || 0) === 0 ? "额度用完自动消耗" : "信用 + 福利",
      color: "#28b8ff",
    },
    {
      title: "日额度",
      value: plainNumber(totalDaily, 0),
      sub: `已用 ${money(snapshot.spend, 2)} / 总计 ${plainNumber(totalDaily, 0)}`,
      color: "#31df9a",
    },
    {
      title: "缓存率",
      value: text(snapshot.cache, "-"),
      sub: "缓存命中 / 请求数",
      color: "#9b7cff",
    },
  ];
}

function renderDots() {
  let html = "";
  for (let i = 0; i < 18; i += 1) {
    const x = 22 + ((i * 53) % (540 - 44));
    const y = 24 + ((i * 37) % (820 - 48));
    const alpha = i % 2 ? 0.07 : 0.1;
    html += `<span class="dot" style="left:${x}px;top:${y}px;background:rgba(255,255,255,${alpha})"></span>`;
  }
  return html;
}

function render() {
  const s = state.snapshot || {};
  if (!s.loggedIn) {
    app.innerHTML = renderLoggedOut(s);
    bindEvents();
    return;
  }
  app.innerHTML = renderMainPanel(s);
  bindEvents();
}

function renderLoggedOut(_s) {
  return `
    <main class="root login-root">
      ${renderLogin(false)}
    </main>
  `;
}

function renderMainPanel(s) {
  const subs = Array.isArray(s.subscriptions) ? s.subscriptions : [];
  return `
    <main class="root">
      <section class="shell">
        <div class="dots">${renderDots()}</div>
        <div class="panel">
          ${renderHeader(s)}
          <div class="error">${escapeHTML(s.err || "")}</div>
          ${renderStats(s)}
          <div class="section-row">
            <div class="section-title">套餐</div>
            <div class="muted">${subs.length} 张</div>
            <div class="spacer"></div>
          </div>
          <div class="scroll">
            <div class="subs">
              ${subs.length ? subs.map(renderSub).join("") : `<div class="empty"></div>`}
            </div>
          </div>
        </div>
      </section>
      ${renderModal()}
    </main>
  `;
}

function renderHeader(s) {
  return `
    <header class="header">
      <div class="logo">◒</div>
      <div class="brand">
        <div class="title">Krill</div>
        <div class="subtitle">额度监控</div>
      </div>
      <div class="time">${escapeHTML(s.timeLabel || "")}</div>
      <div class="spacer"></div>
      <button class="icon-btn" data-action="settings" title="设置">⚙</button>
      <button class="icon-btn" data-action="refresh" title="立即刷新">↻</button>
      <button class="icon-btn" data-action="hide" title="隐藏">—</button>
      <button class="auth-btn" data-action="auth">${s.loggedIn ? "退出登录" : "登录"}</button>
    </header>
  `;
}

function renderStats(s) {
  return `
    <div class="stat-grid">
      ${statCards(s).map((card) => `
        <section class="stat-card">
          <div class="stat-title">${card.title}</div>
          <div class="stat-value" style="color:${card.color}">${escapeHTML(card.value)}</div>
          <div class="stat-sub">${escapeHTML(card.sub)}</div>
        </section>
      `).join("")}
    </div>
  `;
}

function renderSub(sub) {
  const routes = Array.isArray(sub.routes) ? sub.routes.slice(0, 6) : [];
  return `
    <section class="sub-card">
      <div class="sub-top">
        <div class="sub-name">${escapeHTML(text(sub.name, "套餐"))}</div>
        <div class="badge">${escapeHTML(text(sub.daysLeftText, "? 天后到期"))}</div>
      </div>
      <div class="sub-detail">#${escapeHTML(sub.id)}  ·  ${escapeHTML(sub.start)} → ${escapeHTML(sub.end)}</div>
      ${routes.length ? `<div class="routes">${routes.map((route) => `<span class="route">${escapeHTML(route)}</span>`).join("")}</div>` : ""}
      ${renderQuota("转结", sub.forwardedUsed, sub.forwardedLimit, sub.forwardedPercent)}
      ${renderQuota("当日", sub.dailyUsed, sub.dailyLimit, sub.dailyPercent)}
    </section>
  `;
}

function renderQuota(label, used, limit, percent) {
  return `
    <div class="quota">
      <div class="quota-head">
        <span>${label}</span>
        <span>${money(used, 2)} / ${money(limit, 2)}</span>
      </div>
      <div class="bar"><div class="bar-fill" style="width:${pct(percent)}%"></div></div>
    </div>
  `;
}

function renderModal() {
  if (state.modal === "login") {
    return renderLogin();
  }
  if (state.modal === "settings") {
    return renderSettings();
  }
  return `<div class="modal" hidden></div>`;
}

function renderLogin(asModal = true) {
  const err = state.formError || "";
  const provider = state.config.provider || "krill";
  const formName = provider === "newapi" ? "newapi-complete" : "login";
  const title = provider === "newapi" ? "登录 NewAPI" : provider === "sub2" ? "登录 Sub2" : "登录 Krill AI";
  const body = `
    <form class="dialog login" data-form="${formName}">
      <div class="dialog-shell">
        <div class="dots">${renderDialogDots(430, 430)}</div>
        <div class="dialog-content">
          <div class="dialog-brand">
            <div class="login-orb">◒</div>
            <div>
              <div class="dialog-title">${title}</div>
              <div class="subtitle">额度监控</div>
            </div>
            <div class="spacer"></div>
          </div>
          <div class="glass-line"></div>
          ${renderProviderTabs(provider)}
          ${renderProviderLoginFields(provider)}
          <div class="error">${escapeHTML(err)}</div>
          <div class="dialog-buttons">
            <button class="secondary" type="button" data-action="cancel-modal">取消</button>
            ${renderLoginPrimaryButton(provider)}
          </div>
        </div>
      </div>
    </form>
  `;
  if (!asModal) {
    return `<div class="login-stage">${body}</div>`;
  }
  return `
    <div class="modal">
      ${body}
    </div>
  `;
}

function renderProviderTabs(provider) {
  const providers = [
    ["newapi", "NewAPI"],
    ["sub2", "Sub2"],
    ["krill", "Krill AI"],
  ];
  return `
    <div class="provider-tabs">
      ${providers.map(([value, label]) => `
        <button
          class="provider-tab ${provider === value ? "active" : ""}"
          type="button"
          data-action="provider"
          data-provider="${value}"
        >${label}</button>
      `).join("")}
    </div>
  `;
}

function renderProviderLoginFields(provider) {
  if (provider === "newapi") {
    const auto = Boolean(state.config.newapiAutoCallback);
    return `
      <input class="field" name="newapiBaseUrl" autocomplete="url" placeholder="NewAPI 网站地址，例如 https://x666.me" value="${escapeHTML(state.config.newapiBaseUrl || "")}" />
      <button class="oauth-button" type="button" data-action="newapi-start-oauth">${state.busy ? "打开中..." : "使用 LinuxDo 登录"}</button>
      ${!auto && state.oauthAuthorizeUrl ? `<button class="oauth-copy" type="button" data-action="copy-oauth-url">复制授权链接</button>` : ""}
      <label class="check"><input type="checkbox" name="autoCallback" ${auto ? "checked" : ""} />自动完成登录（独立窗口）</label>
      ${!auto ? `<input class="field" name="callbackUrl" autocomplete="off" placeholder="粘贴登录完成后的回调 URL" />` : ""}
      <label class="check"><input type="checkbox" name="remember" ${state.config.rememberLogin ? "checked" : ""} />记住登录状态</label>
      <div class="oauth-note">${escapeHTML(state.oauthMessage || (auto ? "LinuxDo 授权完成后会自动登录；首次可能需要登录，之后会记住。" : "使用当前浏览器登录态打开授权页；完成后请复制地址栏完整回调 URL。"))}</div>
    `;
  }
  if (provider === "sub2") {
    return `
      <div class="provider-placeholder">Sub2 支持暂未开放</div>
      <label class="check"><input type="checkbox" name="remember" ${state.config.rememberLogin ? "checked" : ""} />记住登录状态</label>
    `;
  }
  return `
    <input class="field" name="email" autocomplete="username" placeholder="邮箱" value="${escapeHTML(state.config.email || "")}" />
    <input class="field" name="password" autocomplete="current-password" placeholder="密码" type="password" />
    <label class="check"><input type="checkbox" name="remember" ${state.config.rememberLogin ? "checked" : ""} />记住登录状态</label>
  `;
}

function renderLoginPrimaryButton(provider) {
  if (provider === "newapi") {
    if (state.config.newapiAutoCallback) {
      return "";
    }
    return `<button class="primary" type="submit">${state.busy ? "验证中..." : "完成登录"}</button>`;
  }
  if (provider === "sub2") {
    return `<button class="primary" type="button" disabled>暂未开放</button>`;
  }
  return `<button class="primary" type="submit">${state.busy ? "登录中..." : "登录"}</button>`;
}

function renderSettings() {
  const cfg = state.config || {};
  return `
    <div class="modal">
      <form class="dialog settings" data-form="settings">
        <div class="dialog-shell">
          <div class="dots">${renderDialogDots(360, 320)}</div>
          <div class="dialog-content">
            <div class="dialog-title">设置</div>
            <div class="number-field">
              <input class="field" name="refreshSec" min="3" max="3600" type="number" value="${Number(cfg.refreshSec || 60)}" />
              <span class="field-suffix">秒刷新</span>
            </div>
            <label class="check"><input type="checkbox" name="onTop" ${cfg.onTop ? "checked" : ""} />窗口置顶</label>
            <label class="check"><input type="checkbox" name="glassEnabled" ${cfg.glassEnabled ? "checked" : ""} />显示玻璃球</label>
            <label class="check"><input type="checkbox" name="remember" ${cfg.rememberLogin ? "checked" : ""} />记住登录状态</label>
            <div class="dialog-buttons">
              <button class="secondary" type="button" data-action="cancel-modal">取消</button>
              <button class="primary" type="submit">保存</button>
            </div>
          </div>
        </div>
      </form>
    </div>
  `;
}

function renderDialogDots(w, h) {
  let html = "";
  for (let i = 0; i < 12; i += 1) {
    const x = 22 + ((i * 53) % Math.max(1, w - 44));
    const y = 24 + ((i * 37) % Math.max(1, h - 48));
    html += `<span class="dot" style="left:${x}px;top:${y}px"></span>`;
  }
  return html;
}

function bindEvents() {
  for (const btn of app.querySelectorAll("[data-action]")) {
    btn.addEventListener("click", onAction);
  }
  const login = app.querySelector('[data-form="login"]');
  if (login) {
    login.addEventListener("submit", onLogin);
  }
  const newapi = app.querySelector('[data-form="newapi-complete"]');
  if (newapi) {
    newapi.addEventListener("submit", onNewAPIComplete);
  }
  const settings = app.querySelector('[data-form="settings"]');
  if (settings) {
    settings.addEventListener("submit", onSettings);
  }
}

async function onAction(event) {
  const action = event.currentTarget.dataset.action;
  if (action === "provider") {
    syncLoginFormState(event.currentTarget.closest("form"));
    state.config.provider = event.currentTarget.dataset.provider || "krill";
    state.formError = "";
    state.oauthMessage = "";
    state.oauthAuthorizeUrl = "";
    render();
    return;
  }
  const api = backend();
  if (!api) {
    return;
  }
  if (action === "settings") {
    state.formError = "";
    state.modal = "settings";
    render();
  } else if (action === "refresh") {
    await callRefresh();
  } else if (action === "hide") {
    await api.HidePanel();
  } else if (action === "auth") {
    if (state.snapshot.loggedIn) {
      const snap = await api.Logout();
      state.snapshot = snap;
      state.modal = "login";
      state.formError = "";
      render();
    } else {
      state.formError = "";
      state.modal = "login";
      render();
    }
  } else if (action === "cancel-modal") {
    if (!state.snapshot.loggedIn) {
      await api.HidePanel();
      return;
    }
    state.modal = "";
    state.formError = "";
    render();
  } else if (action === "newapi-start-oauth") {
    await startNewAPIOAuth(event.currentTarget.closest("form"));
  } else if (action === "copy-oauth-url") {
    await copyOAuthAuthorizeURL();
  }
}

async function callRefresh() {
  const api = backend();
  if (!api || state.busy) {
    return;
  }
  state.busy = true;
  try {
    const snap = await api.Refresh();
    state.snapshot = snap;
    if (!snap.loggedIn) {
      state.modal = "login";
    }
  } catch (err) {
    state.snapshot = { ...state.snapshot, err: String(err || "刷新失败") };
  } finally {
    state.busy = false;
    render();
  }
}

async function onLogin(event) {
  event.preventDefault();
  if ((state.config.provider || "krill") !== "krill") {
    return;
  }
  if (state.busy) {
    return;
  }
  const form = new FormData(event.currentTarget);
  const email = text(form.get("email")).trim();
  const password = text(form.get("password"));
  const rememberLogin = form.get("remember") === "on";
  if (!email || !password) {
    state.formError = "请输入邮箱和密码";
    render();
    return;
  }
  state.busy = true;
  state.formError = "";
  render();
  try {
    const snap = await backend().Login({ provider: "krill", email, password, rememberLogin });
    state.snapshot = snap;
    state.config = { ...state.config, email, rememberLogin };
    state.modal = "";
  } catch (err) {
    state.formError = String(err || "登录失败");
    state.modal = "login";
  } finally {
    state.busy = false;
    render();
  }
}

function syncLoginFormState(form) {
  if (!form) {
    return;
  }
  const data = new FormData(form);
  if (form.querySelector('input[name="remember"]')) {
    state.config.rememberLogin = data.get("remember") === "on";
  }
  if (data.has("newapiBaseUrl")) {
    state.config.newapiBaseUrl = text(data.get("newapiBaseUrl")).trim();
  }
  if (form.querySelector('input[name="autoCallback"]')) {
    state.config.newapiAutoCallback = data.get("autoCallback") === "on";
  }
}

async function startNewAPIOAuth(form) {
  if (state.busy) {
    return;
  }
  syncLoginFormState(form);
  const baseUrl = state.config.newapiBaseUrl || "";
  if (!baseUrl) {
    state.formError = "请输入 NewAPI 网站地址";
    render();
    return;
  }
  state.busy = true;
  state.formError = "";
  state.oauthMessage = "";
  state.oauthAuthorizeUrl = "";
  render();
  try {
    const started = await backend().StartNewAPIOAuth({
      baseUrl,
      rememberLogin: state.config.rememberLogin,
      autoCallback: state.config.newapiAutoCallback,
    });
    state.config.provider = "newapi";
    state.config.newapiBaseUrl = started.baseUrl || baseUrl;
    state.oauthAuthorizeUrl = started.autoCapture ? "" : (started.authorizeUrl || "");
    state.oauthMessage = started.autoCapture
      ? "已打开独立授权窗口，LinuxDo 授权完成后会自动登录；首次可能需要登录，之后会记住。"
      : "已用当前浏览器打开授权页。完成后请复制 NewAPI 回调页地址栏完整 URL。";
  } catch (err) {
    state.formError = String(err || "启动 LinuxDo 登录失败");
  } finally {
    state.busy = false;
    render();
  }
}

async function copyOAuthAuthorizeURL() {
  if (!state.oauthAuthorizeUrl) {
    return;
  }
  try {
    await navigator.clipboard.writeText(state.oauthAuthorizeUrl);
    state.oauthMessage = "授权链接已复制。LinuxDo 授权页恢复后，在浏览器打开该链接继续登录。";
  } catch {
    state.oauthMessage = state.oauthAuthorizeUrl;
  }
  render();
}

async function onNewAPIComplete(event) {
  event.preventDefault();
  if (state.busy) {
    return;
  }
  const form = new FormData(event.currentTarget);
  const baseUrl = text(form.get("newapiBaseUrl")).trim();
  const callbackUrl = text(form.get("callbackUrl")).trim();
  const rememberLogin = form.get("remember") === "on";
  if (!baseUrl || !callbackUrl) {
    state.formError = "请输入网站地址并粘贴回调 URL";
    render();
    return;
  }
  state.busy = true;
  state.formError = "";
  render();
  try {
    await completeNewAPIOAuthFromCallback({
      baseUrl,
      callbackUrl,
      rememberLogin,
    });
  } catch (err) {
    state.formError = String(err || "NewAPI 登录失败");
    state.modal = "login";
  } finally {
    state.busy = false;
    render();
  }
}

async function completeNewAPIOAuthFromCallback(payload) {
  const baseUrl = text(payload?.baseUrl || state.config.newapiBaseUrl).trim();
  const callbackUrl = text(payload?.callbackUrl).trim();
  const rememberLogin = typeof payload?.rememberLogin === "boolean" ? payload.rememberLogin : state.config.rememberLogin;
  if (!baseUrl || !callbackUrl) {
    state.formError = "NewAPI 回调 URL 无效";
    state.modal = "login";
    render();
    return;
  }
  state.busy = true;
  state.formError = "";
  state.oauthMessage = "正在完成 NewAPI 登录...";
  render();
  const snap = await backend().CompleteNewAPIOAuth({
    baseUrl,
    callbackUrl,
    rememberLogin,
  });
  state.snapshot = snap;
  state.config = { ...state.config, provider: "newapi", newapiBaseUrl: baseUrl, rememberLogin };
  state.modal = "";
  state.oauthMessage = "";
  state.oauthAuthorizeUrl = "";
}

async function onSettings(event) {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const refreshSec = Math.max(3, Math.min(3600, Number(form.get("refreshSec") || 60)));
  const payload = {
    refreshSec,
    onTop: form.get("onTop") === "on",
    glassEnabled: form.get("glassEnabled") === "on",
    rememberLogin: form.get("remember") === "on",
    provider: state.config.provider || "krill",
    newapiBaseUrl: state.config.newapiBaseUrl || "",
  };
  try {
    const cfg = await backend().SaveSettings(payload);
    state.config = cfg;
    state.modal = "";
  } catch (err) {
    state.formError = String(err || "保存失败");
  }
  render();
}

async function boot() {
  render();
  const api = await waitForBackend();
  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn("snapshot:update", (snap) => {
      state.snapshot = snap;
      if (snap.loggedIn && state.modal === "login") {
        state.modal = "";
      }
      if (!snap.loggedIn) {
        state.modal = "login";
      }
      render();
    });
    window.runtime.EventsOn("oauth:error", (message) => {
      state.formError = String(message || "NewAPI 登录失败");
      state.modal = "login";
      render();
    });
    window.runtime.EventsOn("oauth:callback", async (payload) => {
      try {
        await completeNewAPIOAuthFromCallback(payload);
      } catch (err) {
        state.formError = String(err || "NewAPI 登录失败");
        state.modal = "login";
      } finally {
        state.busy = false;
        render();
      }
    });
  }
  const initial = await api.Bootstrap();
  state.config = { ...state.config, ...initial.config };
  state.config.provider ||= "krill";
  state.config.newapiBaseUrl ||= "";
  state.snapshot = initial.snapshot;
  if (!state.snapshot.loggedIn) {
    state.modal = "login";
  }
  render();
  window.addEventListener("mouseup", () => {
    const active = document.activeElement;
    if (active && ["INPUT", "BUTTON"].includes(active.tagName)) {
      return;
    }
    backend()?.SaveWindowPosition?.();
  });
}

boot().catch((err) => {
  state.snapshot = { ...state.snapshot, err: String(err || "启动失败") };
  render();
});
