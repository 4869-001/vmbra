/* eslint-disable */
(function () {
  "use strict";

  const App = {
    token: (document.querySelector('meta[name="anywhere-token"]') || {}).content || "",
    status: null,
    route: "",
    views: {},
    timer: null,
  };
  window.App = App;

  /* ---------------- 路由定义 ---------------- */
  App.routes = [
    { key: "workstation", label: "工作站", icon: "monitor" },
    { key: "files", label: "文件", icon: "folder" },
    { key: "apps", label: "应用", icon: "grid" },
    { key: "network", label: "网络", icon: "network" },
    { key: "env", label: "环境", icon: "globe" },
    { key: "maintenance", label: "维护与恢复", icon: "shield" },
    { key: "tasks", label: "任务", icon: "list" },
    { key: "diagnostics", label: "诊断", icon: "activity" },
    { key: "settings", label: "设置", icon: "gear" },
  ];

  /* ---------------- 图标 ---------------- */
  const ICONS = {
    monitor: '<rect x="2.5" y="4" width="15" height="10" rx="1.6"/><path d="M7 17h6M10 14v3"/>',
    folder: '<path d="M2.5 6.2A1.7 1.7 0 0 1 4.2 4.5h3.1l1.5 1.8h6.9a1.7 1.7 0 0 1 1.7 1.7v6.3a1.7 1.7 0 0 1-1.7 1.7H4.2a1.7 1.7 0 0 1-1.7-1.7z"/>',
    grid: '<rect x="2.8" y="2.8" width="6" height="6" rx="1.4"/><rect x="11.2" y="2.8" width="6" height="6" rx="1.4"/><rect x="2.8" y="11.2" width="6" height="6" rx="1.4"/><rect x="11.2" y="11.2" width="6" height="6" rx="1.4"/>',
    network: '<rect x="9" y="2.5" width="2" height="3.4"/><rect x="2.6" y="14" width="2" height="3.4"/><rect x="9" y="14" width="2" height="3.4"/><rect x="15.4" y="14" width="2" height="3.4"/><path d="M10 5.9v3.3M3.6 14v-2.2h12.8V14M10 9.2V14"/>',
    globe: '<circle cx="10" cy="10" r="7.2"/><path d="M2.8 10h14.4M10 2.8c1.9 2 2.9 4.6 2.9 7.2S11.9 15.2 10 17.2C8.1 15.2 7.1 12.6 7.1 10S8.1 4.8 10 2.8z"/>',
    shield: '<path d="M10 2.6l6 2.2v4.6c0 3.6-2.4 6.5-6 8-3.6-1.5-6-4.4-6-8V4.8z"/><path d="M10 7.4v3.2M10 13.1v.1"/>',
    list: '<path d="M4 5.5h12M4 10h12M4 14.5h8"/>',
    activity: '<path d="M2.8 10.6h3.1l1.7-5 3 9.4 1.8-5.2h3.8"/>',
    gear: '<circle cx="10" cy="10" r="2.6"/><path d="M10 2.7v1.9M10 15.4v1.9M2.7 10h1.9M15.4 10h1.9M4.9 4.9l1.3 1.3M13.8 13.8l1.3 1.3M15.1 4.9l-1.3 1.3M6.2 13.8l-1.3 1.3"/>',
    refresh: '<path d="M16 10a6 6 0 1 1-1.8-4.3M16 4v4h-4"/>',
    plus: '<path d="M10 4.6v10.8M4.6 10h10.8"/>',
    trash: '<path d="M3.8 5.6h12.4M8 5.6V4a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1v1.6M5.4 5.6l.7 9.6a1.4 1.4 0 0 0 1.4 1.3h5a1.4 1.4 0 0 0 1.4-1.3l.7-9.6"/>',
    download: '<path d="M10 3.4v9.2M6.3 9.2l3.7 3.4 3.7-3.4M4 16h12"/>',
    upload: '<path d="M10 13.4V4.2M6.3 7.6L10 4.2l3.7 3.4M4 16h12"/>',
    play: '<path d="M6.5 4.6l8 5.4-8 5.4z"/>',
    stop: '<rect x="5.6" y="5.6" width="8.8" height="8.8" rx="1.4"/>',
    terminal: '<rect x="2.6" y="3.6" width="14.8" height="12.8" rx="2"/><path d="M5.8 8.2l2.4 2.2-2.4 2.2M10.6 12.8h3.6"/>',
    check: '<circle cx="10" cy="10" r="7.2"/><path d="M6.7 10.2l2.2 2.2 4.3-4.4"/>',
    alert: '<path d="M10 3.4l7 12.4H3z"/><path d="M10 8v3.1M10 13.6v.1"/>',
    close: '<path d="M5.4 5.4l9.2 9.2M14.6 5.4l-9.2 9.2"/>',
    external: '<path d="M11.4 3.8h4.8v4.8M15.6 4.4l-6.4 6.4M14 11.6v3.8a1.2 1.2 0 0 1-1.2 1.2H4.6a1.2 1.2 0 0 1-1.2-1.2V7.2A1.2 1.2 0 0 1 4.6 6h3.8"/>',
    file: '<path d="M5.4 2.8h6l3.2 3.4v10.6a1 1 0 0 1-1 1H5.4a1 1 0 0 1-1-1V3.8a1 1 0 0 1 1-1z"/><path d="M11.2 2.9v3.6h3.3"/>',
    copy: '<rect x="6.6" y="6.6" width="9" height="9" rx="1.6"/><path d="M13.4 6.4V5a1.6 1.6 0 0 0-1.6-1.6H5A1.6 1.6 0 0 0 3.4 5v6.8A1.6 1.6 0 0 0 5 13.4h1.4"/>',
    chevron: '<path d="M6 8l4 4 4-4"/>',
    dot: '<circle cx="10" cy="10" r="3.4"/>',
  };

  App.icon = function (name, size) {
    const d = ICONS[name] || ICONS.dot;
    const s = size || 18;
    return (
      '<svg viewBox="0 0 20 20" width="' + s + '" height="' + s +
      '" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      d + "</svg>"
    );
  };

  /* ---------------- 工具函数 ---------------- */
  App.esc = function (v) {
    return String(v == null ? "" : v).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  };

  App.fmtBytes = function (n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    const units = ["KB", "MB", "GB", "TB"];
    let i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
    return n.toFixed(n >= 10 ? 0 : 1) + " " + units[i];
  };

  App.fmtDuration = function (sec) {
    sec = Math.max(0, Number(sec) || 0);
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = Math.floor(sec % 60);
    if (d > 0) return d + " 天 " + h + " 小时";
    if (h > 0) return h + " 小时 " + m + " 分";
    if (m > 0) return m + " 分 " + s + " 秒";
    return s + " 秒";
  };

  App.fmtTime = function (iso) {
    if (!iso) return "—";
    const d = new Date(iso);
    if (isNaN(d.getTime())) return "—";
    const pad = (x) => String(x).padStart(2, "0");
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
      " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
  };

  App.stateChip = function (state) {
    const map = {
      running: ["运行中", "green"],
      stopped: ["已停止", "gray"],
      starting: ["启动中", "amber"],
      stopping: ["停止中", "amber"],
      error: ["异常", "red"],
    };
    const m = map[state] || [state || "未知", "gray"];
    return '<span class="chip ' + m[1] + '">' + App.esc(m[0]) + "</span>";
  };

  App.toast = function (msg, type) {
    const host = document.getElementById("toasts");
    const el = document.createElement("div");
    el.className = "toast" + (type ? " " + type : "");
    el.textContent = msg;
    host.appendChild(el);
    setTimeout(function () {
      el.style.transition = "opacity .2s ease";
      el.style.opacity = "0";
      setTimeout(() => el.remove(), 220);
    }, type === "err" ? 5200 : 2800);
  };

  /* ---------------- API ---------------- */
  App.api = async function (path, opts) {
    opts = opts || {};
    const headers = { "X-Anywhere-Token": App.token };
    let body = opts.raw;
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(opts.body);
    }
    const res = await fetch(path, { method: opts.method || "GET", headers: headers, body: body });
    const ct = res.headers.get("content-type") || "";
    if (!res.ok) {
      let msg = "请求失败（" + res.status + "）";
      try {
        if (ct.indexOf("json") >= 0) {
          const j = await res.json();
          msg = j.error || msg;
        } else {
          msg = (await res.text()).slice(0, 300) || msg;
        }
      } catch (e) { /* ignore */ }
      throw new Error(msg);
    }
    if (ct.indexOf("json") >= 0) return res.json();
    return res;
  };

  App.wsURL = function (path) {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const sep = path.indexOf("?") >= 0 ? "&" : "?";
    return proto + "//" + location.host + path + sep + "token=" + encodeURIComponent(App.token);
  };

  /* ---------------- 弹层 ---------------- */
  App.modal = function (opts) {
    const root = document.getElementById("modal-root");
    const wrap = document.createElement("div");
    wrap.className = "modal";
    wrap.innerHTML =
      '<div class="modal-card' + (opts.narrow ? " narrow" : "") + '">' +
        '<div class="modal-head"><div class="modal-title">' + App.esc(opts.title || "") + "</div>" +
          '<button class="icon-btn ghost" data-close>' + App.icon("close", 15) + "</button></div>" +
        '<div class="modal-body"></div>' +
        (opts.footer !== false ? '<div class="modal-foot"></div>' : "") +
      "</div>";
    root.appendChild(wrap);
    const card = wrap.querySelector(".modal-card");
    const bodyEl = wrap.querySelector(".modal-body");
    const footEl = wrap.querySelector(".modal-foot");
    if (typeof opts.body === "string") bodyEl.innerHTML = opts.body;
    else if (opts.body) bodyEl.appendChild(opts.body);
    if (footEl && typeof opts.footer === "string") footEl.innerHTML = opts.footer;

    function close() {
      if (typeof opts.onClose === "function") opts.onClose();
      wrap.remove();
      document.removeEventListener("keydown", onKey);
    }
    function onKey(e) { if (e.key === "Escape") close(); }
    document.addEventListener("keydown", onKey);
    wrap.querySelector("[data-close]").addEventListener("click", close);
    wrap.addEventListener("click", function (e) { if (e.target === wrap) close(); });
    return { wrap: wrap, card: card, body: bodyEl, foot: footEl, close: close };
  };

  App.confirm = function (title, message, danger) {
    return new Promise(function (resolve) {
      const m = App.modal({
        title: title,
        narrow: true,
        body: '<div class="card-desc" style="margin:0">' + App.esc(message) + "</div>",
        footer:
          '<button class="btn" data-no>取消</button>' +
          '<button class="btn ' + (danger ? "danger" : "primary") + '" data-yes>确认</button>',
        onClose: function () { resolve(false); },
      });
      m.foot.querySelector("[data-no]").addEventListener("click", m.close);
      m.foot.querySelector("[data-yes]").addEventListener("click", function () {
        resolve(true);
        m.wrap.querySelector("[data-close]").click();
      });
    });
  };

  /* ---------------- 路由渲染 ---------------- */

  // 路由支持参数与别名，供桌面程序一键直达面板：
  //   #/apps?open=terminal · #/terminal · #/desktop · #/network · #/files
  App.parseRoute = function () {
    const raw = location.hash.replace(/^#\/?/, "");
    const parts = raw.split("?");
    const params = new URLSearchParams(parts[1] || "");
    let key = parts[0] || "";
    if (key === "terminal") { params.set("open", "terminal"); key = "apps"; }
    if (key === "desktop") { params.set("open", "desktop"); key = "apps"; }
    return { key: key, params: params };
  };

  // 待执行的一次性动作（打开终端、启动桌面…），消费后清掉，避免刷新时重复触发。
  App.intent = null;

  App.takeIntent = function (name) {
    if (!App.intent || App.intent.get("open") !== name) return false;
    App.intent = null;
    if (location.hash.indexOf("?") >= 0 || /#\/(terminal|desktop)$/.test(location.hash)) {
      history.replaceState(null, "", "#/" + (App.route || "apps"));
    }
    return true;
  };

  function currentKey() {
    const key = App.parseRoute().key;
    const found = App.routes.some((r) => r.key === key);
    return found ? key : "workstation";
  }

  function renderNav() {
    const nav = document.getElementById("nav");
    nav.innerHTML = App.routes
      .map(function (r) {
        return '<a data-route="' + r.key + '" title="' + r.label + '">' + App.icon(r.icon, 17) +
          "<span>" + r.label + "</span></a>";
      })
      .join("");
    nav.addEventListener("click", function (e) {
      const a = e.target.closest("a[data-route]");
      if (!a) return;
      location.hash = "#/" + a.dataset.route;
    });
  }

  App.render = async function () {
    const route = App.parseRoute();
    App.intent = route.params.size > 0 ? route.params : null;
    const key = currentKey();
    App.route = key;
    const meta = App.routes.find((r) => r.key === key);
    document.getElementById("page-title").textContent = meta ? meta.label : "工作站";
    document.querySelectorAll("#nav a").forEach(function (a) {
      a.classList.toggle("active", a.dataset.route === key);
    });
    const view = document.getElementById("view");
    view.innerHTML = '<div class="loading-row"><span class="spinner"></span> 正在加载…</div>';
    const fn = App.views[key];
    if (!fn) {
      view.innerHTML = '<div class="empty">页面未实现</div>';
      return;
    }
    try {
      await fn(view);
    } catch (err) {
      view.innerHTML = '<div class="empty">加载失败：' + App.esc(err.message) + "</div>";
    }
  };

  App.refreshStatus = async function () {
    try {
      const st = await App.api("/api/status");
      App.status = st;
      const dot = document.getElementById("link-dot");
      const text = document.getElementById("link-text");
      dot.className = "dot ok";
      text.textContent = "管理服务已连接";
      const sel = document.getElementById("ws-select");
      if (st.workstation) {
        sel.innerHTML = '<option>' + App.esc(st.workstation.name) + "</option>";
        sel.value = st.workstation.name;
      }
      document.documentElement.setAttribute("data-theme", (st.settings && st.settings.theme) || "light");
      return st;
    } catch (err) {
      const dot = document.getElementById("link-dot");
      dot.className = "dot bad";
      document.getElementById("link-text").textContent = "管理服务已断开";
      return null;
    }
  };

  App.boot = function () {
    // 桌面程序内嵌时带 ?embed=1：隐藏 Web 侧边栏，只保留内容区。
    if (new URLSearchParams(location.search).has("embed")) {
      document.body.classList.add("embed");
    }
    renderNav();
    document.getElementById("btn-refresh").addEventListener("click", function () {
      App.refreshStatus();
      App.render();
    });
    document.getElementById("btn-reconnect").addEventListener("click", async function () {
      const st = await App.refreshStatus();
      if (st) App.toast("已连接到管理服务", "ok");
      App.render();
    });
    window.addEventListener("hashchange", App.render);
    App.refreshStatus().then(function (st) {
      if (st && st.settings && st.settings.theme) {
        document.documentElement.setAttribute("data-theme", st.settings.theme);
      }
    });
    App.render();
    clearInterval(App.timer);
    App.timer = setInterval(App.refreshStatus, 8000);
  };

  /* 常驻任务日志跟踪 */
  App.trackTask = function (taskId, onLog, onDone) {
    let finished = false;
    const ws = new WebSocket(App.wsURL("/api/tasks/" + encodeURIComponent(taskId) + "/ws"));
    ws.onmessage = function (ev) {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      if (msg.type === "snapshot") {
        if (msg.output) onLog(msg.output);
      } else if (msg.type === "output") {
        onLog(msg.data);
      } else if (msg.type === "done") {
        finished = true;
        onDone && onDone(msg.task);
        ws.close();
      }
    };
    ws.onerror = function () { if (!finished) onDone && onDone(null); };
    return ws;
  };
})();
