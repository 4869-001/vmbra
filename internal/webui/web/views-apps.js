/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  // Web 终端：与 VPS SSH 一致的体验——UTF-8、真彩色、断线自动重连、字号可调。
  function terminalModal(kind, system) {
    const termCfg = (system && system.terminal) || {};
    const m = A.modal({
      title: kind === "root" ? "终端 · root" : "终端 · 工作站用户",
      body:
        '<div class="term-bar">' +
          '<span class="dot" id="term-dot"></span>' +
          '<span class="tiny" id="term-state">连接中…</span>' +
          '<span class="tiny muted" id="term-env">' +
            E(termCfg.lang || "") + (termCfg.tz ? " · " + E(termCfg.tz) : "") +
            (termCfg.proxy ? " · 代理 " + E(termCfg.proxy) : "") +
            (termCfg.dns ? " · DNS " + E(termCfg.dns) : "") +
          "</span>" +
          '<span class="spacer"></span>' +
          '<button class="btn small" data-font="-1" title="缩小字号">A-</button>' +
          '<button class="btn small" data-font="1" title="放大字号">A+</button>' +
          '<button class="btn small" data-reconnect>' + A.icon("refresh", 12) + "重连</button>" +
        "</div>" +
        '<div class="term-shell"><div class="term-host" id="term"></div></div>',
      footer: '<span class="tiny muted" style="margin-right:auto">Ctrl+C 中断 · 选中即复制 · ⌘V 粘贴</span>' +
        '<button class="btn" data-close2>关闭</button>',
      onClose: function () {
        closed = true;
        clearTimeout(retryTimer);
        if (ws) try { ws.close(); } catch (e) {}
      },
    });

    let ws = null;
    let closed = false;
    let retryTimer = null;
    let retries = 0;
    const host = m.body.querySelector("#term");
    const dot = m.body.querySelector("#term-dot");
    const state = m.body.querySelector("#term-state");
    const term = new window.Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12.5,
      allowProposedApi: true,
      theme: { background: "#101014", foreground: "#e6e6ea", cursor: "#7db3ff", selectionBackground: "#2a3f5f" },
      cursorBlink: true,
      scrollback: 5000,
      convertEol: false,
    });
    const fit = new window.FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(host);
    try { fit.fit(); } catch (e) {}

    function setState(text, cls) {
      state.textContent = text;
      dot.className = "dot" + (cls ? " " + cls : "");
    }

    function connect() {
      if (closed) return;
      clearTimeout(retryTimer);
      setState("连接中…", "");
      ws = new WebSocket(A.wsURL("/api/apps/terminal?kind=" + encodeURIComponent(kind)));
      ws.binaryType = "arraybuffer";
      ws.onopen = function () {
        retries = 0;
        setState("已连接", "ok");
        try { fit.fit(); } catch (e) {}
        sendResize();
        term.focus();
      };
      ws.onmessage = function (ev) {
        if (typeof ev.data === "string") {
          try {
            const msg = JSON.parse(ev.data);
            if (msg.type === "exit") {
              setState("会话已结束", "");
              term.write("\r\n\x1b[90m[会话已结束]\x1b[0m\r\n");
            }
            if (msg.type === "error") term.write("\r\n\x1b[31m" + msg.message + "\x1b[0m\r\n");
          } catch (e) {}
          return;
        }
        term.write(new Uint8Array(ev.data));
      };
      ws.onclose = function () {
        if (closed) return;
        retries += 1;
        const wait = Math.min(8, retries * 2);
        setState("连接断开，" + wait + " 秒后重连", "bad");
        term.write("\r\n\x1b[90m[连接已断开，将自动重连；工作站停止时请先启动]\x1b[0m\r\n");
        retryTimer = setTimeout(connect, wait * 1000);
      };
      ws.onerror = function () {
        setState("无法建立会话", "bad");
      };
    }

    term.onData(function (data) {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    function sendResize() {
      if (!ws || ws.readyState !== WebSocket.OPEN) return;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    }

    const onResize = function () { try { fit.fit(); sendResize(); } catch (e) {} };
    window.addEventListener("resize", onResize);
    m.foot.querySelector("[data-close2]").addEventListener("click", function () {
      window.removeEventListener("resize", onResize);
      m.close();
    });
    m.wrap.addEventListener("click", function (e) {
      if (e.target === m.wrap) window.removeEventListener("resize", onResize);
    });
    m.body.querySelector("[data-reconnect]").addEventListener("click", function () {
      retries = 0;
      if (ws) try { ws.close(); } catch (e) {}
      connect();
    });
    m.body.querySelectorAll("[data-font]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        const next = (term.options.fontSize || 12.5) + Number(btn.dataset.font);
        term.options.fontSize = Math.max(9, Math.min(22, next));
        try { fit.fit(); sendResize(); } catch (e) {}
      });
    });

    connect();
  }

  function macScreenModal() {
    const m = A.modal({
      title: "Mac 屏幕",
      body:
        '<div class="hint-box" style="margin-bottom:10px">仅查看画面，不注入键鼠事件。需要「屏幕录制」权限。</div>' +
        '<div id="shot-wrap"><div class="loading-row"><span class="spinner"></span> 正在截屏…</div></div>',
      footer: '<button class="btn" data-close3>关闭</button>',
    });
    const wrap = m.body.querySelector("#shot-wrap");
    let url = null;
    let timer = null;

    async function shot() {
      try {
        const res = await fetch("/api/mac/screen", { headers: { "X-Anywhere-Token": A.token } });
        const warn = res.headers.get("X-Anywhere-Warning");
        if (!res.ok) throw new Error((await res.json()).error || "截屏失败");
        const blob = await res.blob();
        if (url) URL.revokeObjectURL(url);
        url = URL.createObjectURL(blob);
        wrap.innerHTML = '<img class="screen-shot" src="' + url + '" alt="Mac 屏幕" />' +
          (warn ? '<div class="hint-box" style="margin-top:10px">' + E(warn) + "</div>" : "") +
          '<div class="tiny muted" style="margin-top:8px">每 5 秒自动刷新 · ' + new Date().toLocaleTimeString() + "</div>";
      } catch (err) {
        wrap.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>";
      }
    }
    shot();
    timer = setInterval(shot, 5000);
    m.foot.querySelector("[data-close3]").addEventListener("click", function () {
      clearInterval(timer);
      if (url) URL.revokeObjectURL(url);
      m.close();
    });
    m.wrap.addEventListener("click", function (e) {
      if (e.target === m.wrap) { clearInterval(timer); if (url) URL.revokeObjectURL(url); }
    });
  }

  async function desktopModal() {
    const m = A.modal({
      title: "图形桌面",
      body: '<div class="term-shell" style="padding:0;background:#000"><div id="vnc-host" style="height:520px"></div></div>',
      footer: '<span class="tiny muted" style="margin-right:auto" id="vnc-status">正在连接 VNC…</span>' +
        '<button class="btn" data-close4>关闭</button>',
    });
    const status = m.foot.querySelector("#vnc-status");
    let rfb = null;
    try {
      const mod = await import("/vendor/novnc/rfb.js");
      const RFB = mod.default;
      const info = await A.api("/api/status");
      const creds = (info.desktop && info.desktop.vnc) || {};
      rfb = new RFB(m.body.querySelector("#vnc-host"), A.wsURL("/api/apps/desktop/ws"), {
        credentials: { password: creds.password || "" },
      });
      rfb.scaleViewport = creds.scale !== "100";
      rfb.background = "#000";
      rfb.addEventListener("connect", function () { status.textContent = "已连接"; });
      rfb.addEventListener("disconnect", function (e) {
        status.textContent = "连接已断开" + (e.detail && e.detail.clean ? "" : "（异常）");
      });
    } catch (err) {
      status.textContent = "无法加载桌面组件";
      m.body.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>";
    }
    m.foot.querySelector("[data-close4]").addEventListener("click", function () {
      if (rfb) try { rfb.disconnect(); } catch (e) {}
      m.close();
    });
  }

  function taskLogModal(title, taskId) {
    const m = A.modal({
      title: title,
      body: '<div class="log" id="task-log"></div>',
      footer: '<span class="tiny muted" style="margin-right:auto" id="task-status">执行中…</span>' +
        '<button class="btn" data-close5>关闭</button>',
    });
    const log = m.body.querySelector("#task-log");
    const st = m.foot.querySelector("#task-status");
    const ws = A.trackTask(taskId, function (chunk) {
      log.textContent += chunk;
      log.scrollTop = log.scrollHeight;
    }, function (task) {
      if (!task) { st.textContent = "任务状态未知"; return; }
      st.textContent = (task.status === "succeeded" ? "已完成" : task.status === "failed" ? "失败：" + (task.error || "") : task.status);
      st.className = "tiny " + (task.status === "succeeded" ? "muted" : "");
    });
    m.foot.querySelector("[data-close5]").addEventListener("click", function () { try { ws.close(); } catch (e) {} m.close(); });
  }

  A.views.apps = async function (root) {
    const [data, system, browserInfo] = await Promise.all([
      A.api("/api/apps"),
      A.api("/api/system"),
      A.api("/api/browser"),
    ]);
    const desktop = data.desktop || {};
    const mac = data.mac || {};
    const browserCfg = browserInfo.config || {};
    const browserVPS = browserInfo.vps || {};
    const ws = data.workstation || {};
    const deb = data.deb || {};
    const termCfg = system.terminal || {};
    const desktopRunning = desktop.state === "running";
    const desktopStateText = desktopRunning ? "运行中" : "已停止";

    // 工作区里可运行的脚本（根目录与 scripts/）
    const candidates = [];
    for (const dir of ["", "scripts"]) {
      try {
        const list = await A.api("/api/files?path=" + encodeURIComponent(dir));
        for (const f of list.entries || []) {
          if (!f.dir && /\.(sh|zsh|bash|py|js|mjs|rb|pl)$/i.test(f.name)) candidates.push(f.path);
        }
      } catch (e) { /* 忽略：目录不存在时不影响页面 */ }
    }

    root.innerHTML =
      // 图形桌面
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          "<div>" +
            '<div class="card-title">图形桌面</div>' +
            '<div class="card-sub">' + E(desktopStateText) + "</div>" +
          "</div>" +
          (desktopRunning
            ? '<button class="btn" data-act="desktop-stop">停止桌面</button>'
            : '<button class="btn" data-act="desktop-start"' + (desktop.available ? "" : ' title="点击查看启用方式"') + ">启动桌面</button>") +
        "</div>" +
        (desktop.available ? "" : '<div class="hint-box" style="margin-top:12px">' + E(desktop.reason || "") +
          ' · <a href="#/env" style="color:inherit">前往「环境」配置 VNC</a></div>') +
      "</section>" +

      // 终端
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div><div class="card-title">终端</div>' +
            '<div class="card-sub">' + E((data.terminal && data.terminal.note) || "") + "</div></div>" +
          '<div class="row">' +
            '<button class="btn" data-act="term-user" ' + (ws.state === "running" ? "" : "disabled") + ">工作站用户</button>" +
            '<button class="btn" data-act="term-root" ' + (ws.state === "running" ? "" : "disabled") + ">root</button>" +
          "</div>" +
        "</div>" +
        '<div class="card-body row tiny muted" style="gap:14px;flex-wrap:wrap">' +
          "<span>语言 " + E(termCfg.lang || "—") + "</span>" +
          "<span>时区 " + E(termCfg.tz || "—") + "</span>" +
          "<span>编码 " + E(termCfg.encoding || "—") + "</span>" +
          "<span>DNS " + E(termCfg.dns || "系统解析器") + "</span>" +
          "<span>代理 " + E(termCfg.proxy || "未设置") + "</span>" +
        "</div>" +
      "</section>" +

      // 受控浏览器
      '<section class="card">' +
        '<div class="card-head">' +
          '<div style="max-width:620px"><div class="card-title">受控浏览器</div>' +
            '<div class="card-desc">独立 profile 启动 Chrome，界面语言、Accept-Language 与时区固定；出口可强制走 VPS，避免时区/语言与出口地区不一致。</div></div>' +
          '<div class="row">' +
            '<span class="chip ' + (browserInfo.running ? "green" : "gray") + '">' +
              (browserInfo.running ? "运行中" : "未启动") + "</span>" +
            '<button class="btn primary" data-act="browser-open"' +
              (browserInfo.available ? "" : ' title="未检测到 Chrome"') + ">" +
              A.icon("external", 13) + "启动浏览器</button>" +
          "</div>" +
        "</div>" +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>界面语言</label><input class="input mono" id="browser-lang" value="' + E(browserCfg.lang || "en-US") + '" placeholder="en-US" /></div>' +
            '<div class="field"><label>时区</label><input class="input mono" id="browser-tz" value="' + E(browserCfg.timeZone || "America/Los_Angeles") + '" placeholder="America/Los_Angeles" /></div>' +
            '<div class="field"><label>出口</label><label class="row tiny" style="height:32px;gap:8px">' +
              '<button class="switch ' + (browserCfg.useVpsEgress ? "on" : "") + '" data-toggle="browser-vps"></button>' +
              '<span class="muted">' +
                (browserCfg.useVpsEgress
                  ? (browserVPS.running ? "VPS SOCKS5 " + E(browserVPS.socks || "") : "跟随 VPS（隧道未运行）")
                  : "跟随系统网络") +
              "</span></label></div>" +
          "</div>" +
          '<div class="form-actions">' +
            '<span class="tiny muted" style="margin-right:auto">Accept-Language：' + E(browserInfo.acceptLanguage || "—") +
              " · 配置目录：" + E(browserInfo.profile || "—") + "</span>" +
            '<button class="btn" data-act="browser-verify">验证语言/时区</button>' +
            '<button class="btn" data-act="browser-save">保存配置</button>' +
          "</div>" +
          (browserInfo.available ? "" : '<div class="hint-box" style="margin-top:10px">' + E(browserInfo.reason || "") + "</div>") +
          (browserInfo.running ? '<div class="hint-box" style="margin-top:10px">该 profile 已在运行；修改语言/时区后需要退出它再启动才会生效。</div>' : "") +
          (browserCfg.useVpsEgress && !browserVPS.running
            ? '<div class="hint-box" style="margin-top:10px">VPS 隧道未运行：启动后浏览器会走宿主网络；时区与语言仍按配置固定。要保证出口也一致，请先到「网络 → VPS 出入口」启动隧道。</div>'
            : "") +
        "</div>" +
      "</section>" +

      // 运行脚本
      '<section class="card">' +
        '<div class="card-head">' +
          '<div><div class="card-title">运行脚本</div>' +
            '<div class="card-desc">在工作站隔离环境里执行脚本，默认允许、无需确认；输出实时回传。</div></div>' +
          '<span class="chip ' + (ws.state === "running" ? "green" : "gray") + '">' +
            (ws.state === "running" ? "可运行" : "工作站未运行") + "</span>" +
        "</div>" +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>脚本</label>' +
              '<input class="input mono" id="run-path" list="script-list" placeholder="scripts/hello.sh" value="' +
                E(candidates[0] || "scripts/hello.sh") + '" />' +
              '<datalist id="script-list">' +
                candidates.map(function (c) { return '<option value="' + E(c) + '"></option>'; }).join("") +
              "</datalist></div>" +
            '<div class="field"><label>参数（空格分隔）</label><input class="input mono" id="run-args" placeholder="--flag value" /></div>' +
            '<div class="field"><label>超时（秒）</label><input class="input" id="run-tmo" type="number" min="1" max="3600" value="300" /></div>' +
          "</div>" +
          '<div class="form-actions">' +
            '<span class="tiny muted" style="margin-right:auto">出网：' +
              (system.network ? "已开启（遵循「网络」页的代理与 DNS 配置）" : "未开启，脚本内无法访问外网") + "</span>" +
            '<button class="btn primary" data-act="run-script" ' + (ws.state === "running" ? "" : "disabled") + ">" +
              A.icon("play", 13) + "运行</button>" +
          "</div>" +
        "</div>" +
      "</section>" +

      // 操控 Mac
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div style="max-width:620px">' +
            '<div class="card-title">操控 Mac (Beta)</div>' +
            '<div class="card-desc">让这台工作站里的 Claude Code、Codex 查看 Mac 屏幕并操作应用。</div>' +
          "</div>" +
          '<div class="row">' +
            '<span class="chip ' + (mac.enabled ? "green" : "gray") + '">' + (mac.enabled ? "已开放" : "已关闭") + "</span>" +
            '<button class="switch ' + (mac.enabled ? "on" : "") + '" data-toggle="mac" aria-label="操控 Mac 开关"></button>' +
          "</div>" +
        "</div>" +
        '<div class="card-divider">' +
          '<div class="card-head">' +
            '<div style="max-width:620px">' +
              '<div class="card-title">允许执行 Mac 命令</div>' +
              '<div class="card-desc">' + E(mac.detail || "额外提供 run_command，以你的 Mac 用户身份运行命令。命令内容不会被记录。") + "</div>" +
            "</div>" +
            '<button class="switch ' + (mac.allowRunCommand ? "on" : "") + '" data-toggle="allow-run"' +
              (mac.enabled ? "" : " disabled") + ' aria-label="允许执行 Mac 命令开关"></button>' +
          "</div>" +
          '<div class="row" style="margin-top:12px">' +
            '<button class="btn small" data-act="mac-screen">' + A.icon("monitor", 13) + "查看 Mac 屏幕</button>" +
            '<button class="btn small" data-act="mac-run">' + A.icon("terminal", 13) + "执行命令" + "</button>" +
            '<button class="btn small" data-act="mac-audit">' + A.icon("list", 13) + "审计记录</button>" +
            '<span class="tiny muted">屏幕权限：' +
              (mac.screenPermission === "ok" ? "已授权" : mac.screenPermission === "denied" ? "未授权" : "未检测") + "</span>" +
          "</div>" +
        "</div>" +
      "</section>" +

      // 安装应用
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div><div class="card-title">安装应用 (.deb)</div></div>' +
          '<button class="btn small ghost" data-act="deb-toggle">展开 ' + A.icon("chevron", 12) + "</button>" +
        "</div>" +
        '<div class="collapse-body" id="deb-body" style="display:none">' +
          '<div class="hint-box" style="margin-bottom:12px">' + E(deb.hint || "") + "</div>" +
          '<div class="row">' +
            '<input type="file" id="deb-input" accept=".deb" class="input" style="flex:1;padding:5px 8px" />' +
            '<button class="btn primary" data-act="deb-upload" ' + (ws.state === "running" ? "" : "disabled") + ">上传并解析</button>" +
          "</div>" +
        "</div>" +
      "</section>";

    root.querySelector('[data-act="desktop-start"]')?.addEventListener("click", async function () {
      if (!desktop.available) {
        const m = A.modal({
          title: "启用图形桌面",
          narrow: true,
          body:
            '<div class="card-desc" style="margin:0 0 12px">' + E(desktop.reason || "当前没有可用的图形后端") + "</div>" +
            '<div class="hint-box">本地进程隔离模式只隔离文件与网络，不提供图形栈。推荐路径：<br />' +
            "1. 安装 Lima 并在其中运行 Linux 桌面（如 Xfce）；<br />" +
            "2. 在工作站内启动 VNC 服务（TigerVNC / x11vnc，端口 5900）；<br />" +
            "3. 在「环境 → 图形桌面接入」填写 127.0.0.1:5900，即可在本页直接操作桌面。</div>",
          footer: '<button class="btn" data-c>知道了</button><button class="btn primary" data-go>前往环境配置</button>',
        });
        m.foot.querySelector("[data-c]").addEventListener("click", m.close);
        m.foot.querySelector("[data-go]").addEventListener("click", function () {
          m.close();
          location.hash = "#/env";
        });
        return;
      }
      try {
        await A.api("/api/apps/desktop/start", { method: "POST" });
        A.toast("桌面会话已建立", "ok");
        desktopModal();
        A.render();
      } catch (err) {
        A.toast(err.message, "err");
      }
    });

    root.querySelector('[data-act="desktop-stop"]')?.addEventListener("click", async function () {
      try {
        await A.api("/api/apps/desktop/stop", { method: "POST" });
        A.toast("桌面会话已结束", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="term-user"]')?.addEventListener("click", function () { terminalModal("user", system); });
    root.querySelector('[data-act="term-root"]')?.addEventListener("click", function () {
      const t = data.terminal || {};
      if (!t.available) { A.toast("工作站未运行，请先启动工作站", "err"); return; }
      if (!t.rootAvailable) {
        const m = A.modal({
          title: "root 终端不可用",
          narrow: true,
          body: '<div class="card-desc" style="margin:0">' + E(t.rootReason || "当前隔离后端不提供 root 会话") + "</div>",
          footer: '<button class="btn" data-c>知道了</button>',
        });
        m.foot.querySelector("[data-c]").addEventListener("click", function () { m.close(); });
        return;
      }
      terminalModal("root", system);
    });

    // ---- 受控浏览器 ----
    function browserBody(useVps) {
      return {
        lang: root.querySelector("#browser-lang").value.trim(),
        timeZone: root.querySelector("#browser-tz").value.trim(),
        useVpsEgress: useVps,
      };
    }

    async function saveBrowser(useVps) {
      try {
        await A.api("/api/browser", { method: "PUT", body: browserBody(useVps) });
        return true;
      } catch (err) {
        A.toast(err.message, "err");
        return false;
      }
    }

    root.querySelector('[data-toggle="browser-vps"]')?.addEventListener("click", async function () {
      if (await saveBrowser(!browserCfg.useVpsEgress)) A.render();
    });

    root.querySelector('[data-act="browser-verify"]')?.addEventListener("click", async function () {
      const btn = this;
      btn.disabled = true;
      btn.textContent = "验证中…";
      try {
        const res = await A.api("/api/browser/verify", { method: "POST" });
        const a = res.actual || {};
        const expected = res.expected || {};
        const rows =
          '<table class="table"><thead><tr><th>项</th><th>期望</th><th>Chrome 实际</th></tr></thead><tbody>' +
          "<tr><td>时区</td><td class=\"mono\">" + E(expected.timeZone || "") + '</td><td class="mono">' + E(a.timeZone || "") + "</td></tr>" +
          "<tr><td>UTC 偏移</td><td class=\"mono\">—</td><td class=\"mono\">" + E(a.offset) + " 分钟</td></tr>" +
          "<tr><td>界面语言</td><td class=\"mono\">" + E(expected.lang || "") + '</td><td class="mono">' + E(a.language || "") + "</td></tr>" +
          "<tr><td>Accept-Language</td><td class=\"mono\">" + E(res.acceptLang || "") + '</td><td class="mono">' + E((a.languages || []).join(", ")) + "</td></tr>" +
          "</tbody></table>";
        const m = A.modal({
          title: "浏览器自检",
          body: rows + '<div class="hint-box" style="margin-top:10px">' +
            (res.ok
              ? "语言与时区均与配置一致，浏览器指纹不会再与出口地区冲突。"
              : "实际值与期望不一致：可能是 Chrome 版本不识别该语言/时区，换一个值再试。") + "</div>",
          footer: '<button class="btn" data-c>关闭</button>',
        });
        m.foot.querySelector("[data-c]").addEventListener("click", m.close);
      } catch (err) {
        A.toast(err.message, "err");
      }
      btn.disabled = false;
      btn.textContent = "验证语言/时区";
    });

    root.querySelector('[data-act="browser-save"]')?.addEventListener("click", async function () {
      if (await saveBrowser(!!browserCfg.useVpsEgress)) {
        A.toast("浏览器配置已保存", "ok");
        A.render();
      }
    });

    root.querySelector('[data-act="browser-open"]')?.addEventListener("click", async function () {
      if (!browserInfo.available) {
        A.toast(browserInfo.reason || "未找到 Chrome", "err");
        return;
      }
      const btn = this;
      btn.disabled = true;
      try {
        if (await saveBrowser(!!browserCfg.useVpsEgress)) {
          const res = await A.api("/api/browser/open", { method: "POST" });
          const base = res.alreadyRunning
            ? "该 profile 已在运行；改动语言/时区需先退出它"
            : "已启动受控浏览器（" + (res.lang || "") + " · " + (res.timeZone || "") + "）";
          A.toast(res.warning ? base + "；" + res.warning : base, "ok");
          A.render();
        }
      } catch (err) {
        A.toast(err.message, "err");
      }
      btn.disabled = false;
    });

    root.querySelector('[data-act="run-script"]')?.addEventListener("click", async function () {
      const path = root.querySelector("#run-path").value.trim();
      if (!path) { A.toast("请填写脚本路径", "err"); return; }
      const args = root.querySelector("#run-args").value
        .split(/\s+/)
        .map(function (s) { return s.trim(); })
        .filter(Boolean);
      try {
        const res = await A.api("/api/apps/run", {
          method: "POST",
          body: { path: path, args: args, timeoutSec: Number(root.querySelector("#run-tmo").value) || 300 },
        });
        taskLogModal("运行 " + path.split("/").pop(), res.taskId);
      } catch (err) {
        A.toast(err.message, "err");
      }
    });

    async function patchSettings(patch) {
      try {
        const res = await A.api("/api/settings", { method: "PATCH", body: patch });
        A.toast("设置已更新", "ok");
        return res.settings;
      } catch (err) {
        A.toast(err.message, "err");
        return null;
      }
    }

    root.querySelector('[data-toggle="mac"]').addEventListener("click", async function () {
      const next = !mac.enabled;
      const st = await patchSettings({ macControlEnabled: next });
      if (st) A.render();
      void st;
    });

    // 默认允许执行脚本与命令：切换即刻生效，不再二次确认。
    root.querySelector('[data-toggle="allow-run"]').addEventListener("click", async function () {
      if (!mac.enabled) { A.toast("请先开启「操控 Mac」", "err"); return; }
      const st = await patchSettings({ allowMacRunCommand: !mac.allowRunCommand });
      if (st) A.render();
    });

    root.querySelector('[data-act="mac-screen"]').addEventListener("click", macScreenModal);

    root.querySelector('[data-act="mac-audit"]').addEventListener("click", async function () {
      let entries = [];
      try {
        const res = await A.api("/api/mac/audit");
        entries = res.entries || [];
      } catch (err) {
        A.toast(err.message, "err");
        return;
      }
      const rows = entries.length
        ? entries.map(function (e) {
            const code = Number(e.exitCode);
            return "<tr>" +
              '<td class="muted">' + A.fmtTime(e.at) + "</td>" +
              '<td class="mono tiny">' + E(e.action) + "</td>" +
              '<td class="mono tiny">' + E(e.digest || "—") + "</td>" +
              '<td><span class="chip ' + (code === 0 ? "green" : "red") + '">' + E(code) + "</span></td>" +
              '<td class="muted">' + E(e.durationMs) + " ms</td>" +
              '<td class="muted">' + (e.allowed === false ? "拒绝" : "允许") + "</td>" +
            "</tr>";
          }).join("")
        : '<tr><td colspan="6"><div class="empty">还没有审计记录</div></td></tr>';
      const m = A.modal({
        title: "操控 Mac 审计记录",
        body:
          '<div class="card-desc" style="margin:0 0 10px">只记录时间、命令摘要哈希、退出码与耗时，不记录命令原文；最多保留最近 200 条。</div>' +
          '<div style="max-height:420px;overflow:auto">' +
            '<table class="table"><thead><tr><th>时间</th><th>动作</th><th>命令摘要</th><th>退出码</th><th>耗时</th><th>结果</th></tr></thead>' +
            "<tbody>" + rows + "</tbody></table>" +
          "</div>",
        footer: '<button class="btn" data-c>关闭</button>',
      });
      m.foot.querySelector("[data-c]").addEventListener("click", m.close);
    });

    root.querySelector('[data-act="mac-run"]').addEventListener("click", function () {
      const m = A.modal({
        title: "执行 Mac 命令",
        body:
          '<div class="hint-box" style="margin-bottom:10px">以当前 Mac 用户身份运行；命令内容不会写入审计记录，仅记录摘要哈希。</div>' +
          '<div class="field" style="margin-bottom:10px"><label>命令</label>' +
            '<textarea class="input" id="cmd" style="height:72px" placeholder="例如：ls -la ~/Projects"></textarea></div>' +
          '<div class="grid-2"><div class="field"><label>工作目录（可选）</label><input class="input" id="cwd" placeholder="默认用户主目录" /></div>' +
          '<div class="field"><label>超时（秒）</label><input class="input" id="tmo" type="number" value="30" min="1" max="120" /></div></div>' +
          '<div class="log" id="out" style="margin-top:12px;height:220px"></div>',
        footer: '<button class="btn" data-run-cancel>取消</button><button class="btn primary" data-run>执行</button>',
      });
      m.foot.querySelector("[data-run-cancel]").addEventListener("click", m.close);
      m.foot.querySelector("[data-run]").addEventListener("click", async function () {
        const out = m.body.querySelector("#out");
        out.textContent = "执行中…";
        try {
          const res = await A.api("/api/mac/run", {
            method: "POST",
            body: {
              command: m.body.querySelector("#cmd").value,
              cwd: m.body.querySelector("#cwd").value,
              timeoutSec: Number(m.body.querySelector("#tmo").value) || 30,
            },
          });
          out.textContent = (res.output || "(无输出)") + "\n\n[退出码 " + res.exitCode + " · " + res.durationMs + "ms]";
        } catch (err) {
          out.textContent = "失败：" + err.message;
        }
      });
    });

    root.querySelector('[data-act="deb-toggle"]').addEventListener("click", function (e) {
      const body = root.querySelector("#deb-body");
      const open = body.style.display !== "none";
      body.style.display = open ? "none" : "block";
      e.currentTarget.innerHTML = (open ? "展开 " : "收起 ") + (open ? A.icon("chevron", 12) : A.icon("chevron", 12));
    });

    root.querySelector('[data-act="deb-upload"]')?.addEventListener("click", async function () {
      const input = root.querySelector("#deb-input");
      if (!input.files || !input.files[0]) { A.toast("请选择 .deb 文件", "err"); return; }
      const fd = new FormData();
      fd.append("file", input.files[0]);
      try {
        const res = await A.api("/api/apps/deb", { method: "POST", raw: fd });
        taskLogModal("安装应用 · " + res.filename, res.taskId);
      } catch (err) { A.toast(err.message, "err"); }
    });

    // 快捷入口：#/terminal 与 #/desktop（桌面程序工具栏用），语义是「直接进入该面板」。
    if (A.takeIntent("terminal")) {
      if (ws.state === "running") terminalModal("user", system);
      else A.toast("工作站未运行，正在启动后再打开终端", "err");
    } else if (A.takeIntent("desktop")) {
      const startBtn = root.querySelector('[data-act="desktop-start"]');
      if (startBtn) startBtn.click();
    }
  };
})();
