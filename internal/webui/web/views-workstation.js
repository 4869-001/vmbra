/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  // 一张快捷入口卡片：图标 + 标题 + 说明 + 状态。
  function quickCard(href, icon, title, desc, ready) {
    return (
      '<a class="quick-card" href="' + href + '">' +
      '<span class="quick-icon">' + A.icon(icon, 20) + "</span>" +
      '<span class="quick-title">' + title +
      '<span class="chip ' + (ready ? "green" : "gray") + '">' + (ready ? "就绪" : "待启动") + "</span></span>" +
      '<span class="quick-desc">' + A.esc(desc) + "</span>" +
      "</a>"
    );
  }

  A.views.workstation = async function (root) {
    const [st, env, system, list, licData] = await Promise.all([
      A.api("/api/status"),
      A.api("/api/env"),
      A.api("/api/system"),
      A.api("/api/workstations"),
      A.api("/api/license/status"),
    ]);
    const setupInfo = (licData && licData.setup) || {};
    const licInfo = (licData && licData.license) || {};
    const sys = system.terminal || {};
    const dnsStats = system.dnsStats || {};
    const ws = st.workstation || {};
    const engine = st.engine || {};
    const running = ws.state === "running";
    const images = env.images || [];
    const all = list.workstations || [];
    const desktopReady = !!(st.desktop && st.desktop.available);

    root.innerHTML =
      // 快捷入口：四张卡片
      '<section class="quick-grid">' +
        quickCard("#/terminal", "terminal", "终端", "沙箱内 PTY · UTF-8 · 固定时区", running) +
        quickCard("#/desktop", "monitor", "桌面", desktopReady ? "VNC 已接入" : "需接入虚拟机 VNC", desktopReady && running) +
        quickCard("#/network", "network", "网络", ws.networkLabel || "出网与端口转发", true) +
        quickCard("#/files", "folder", "文件", "工作站工作区（隔离文件系统）", running) +
      "</section>" +

      // 工作站集合
      '<section class="card">' +
        '<div class="card-head">' +
          '<div><div class="card-title">工作站</div>' +
            '<div class="card-sub">每套环境有独立工作区与语言/时区/出口；同一时刻只激活一台</div></div>' +
          '<div class="row"><button class="btn" data-act="ws-new">' + A.icon("plus", 14) + "新建工作站</button></div>" +
        "</div>" +
        '<div class="card-body" style="padding-top:4px">' +
          all.map(function (w) {
            return '<div class="ws-row' + (w.active ? " active" : "") + '">' +
              '<div class="ws-main">' +
                '<div class="ws-name">' + E(w.name) +
                  (w.active ? ' <span class="chip blue">当前</span>' : "") +
                  ' <span class="chip ' + (w.state === "running" ? "green" : "gray") + '">' +
                    (w.state === "running" ? "运行中" : "已停止") + "</span></div>" +
                '<div class="ws-sub mono">' + E(w.workspace) + "</div>" +
                '<div class="ws-sub">' + E(w.networkLabel) + " · " + E(w.lang) + " · " + E(w.timeZone) +
                  " · " + w.cpus + " 核 / " + w.memoryMB + " MB</div>" +
              "</div>" +
              '<div class="row">' +
                (w.active ? "" : '<button class="btn small" data-act="ws-activate" data-id="' + E(w.id) + '">切换</button>') +
                (all.length > 1 ? '<button class="btn small danger" data-act="ws-delete" data-id="' + E(w.id) + '">删除</button>' : "") +
              "</div>" +
            "</div>";
          }).join("") +
        "</div>" +
      "</section>" +

      // 状态
      '<section class="card">' +
        '<div class="card-head">' +
          '<div>' +
            '<div class="card-title">' + E(ws.name) + " " + A.stateChip(ws.state) + "</div>" +
            '<div class="card-sub">' + E(ws.message || "") + "</div>" +
          "</div>" +
          '<div class="row">' +
            (running
              ? '<button class="btn" data-act="stop">' + A.icon("stop", 15) + "停止</button>"
              : '<button class="btn primary" data-act="start">' + A.icon("play", 15) + "启动</button>") +
            '<button class="btn" data-act="restart">' + A.icon("refresh", 15) + "重启</button>" +
          "</div>" +
        "</div>" +
        '<div class="card-body stats">' +
          '<div class="stat"><div class="k">运行时长</div><div class="v">' + (running ? A.fmtDuration(ws.uptimeSec) : "—") + "</div></div>" +
          '<div class="stat"><div class="k">活跃会话</div><div class="v">' + (ws.sessions || 0) + "</div></div>" +
          '<div class="stat"><div class="k">工作区占用</div><div class="v">' + A.fmtBytes(ws.workspaceBytes) + "</div></div>" +
          '<div class="stat"><div class="k">家目录占用</div><div class="v">' + A.fmtBytes(ws.homeBytes) + "</div></div>" +
        "</div>" +
      "</section>" +

      // 环境准备：一键安装 Claude Code 运行所需的软件
      '<section class="card">' +
        '<div class="card-head">' +
          '<div><div class="card-title">环境准备</div>' +
            '<div class="card-sub">自动安装 Git / Node.js / Claude Code，并校验 VPS 出口连通性</div></div>' +
          '<div class="row">' +
            '<button class="btn primary" data-act="setup"' + (setupInfo.scriptReady ? "" : " disabled") + ">" +
              A.icon("gear", 15) + "一键配置环境</button>" +
          "</div>" +
        "</div>" +
        '<div class="card-body">' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">授权状态</span>' +
            '<span class="mono">' + (licInfo.usable ? "已授权（" + E(licInfo.planLabel || "") + "）" : "未激活，请先在「设置」页激活") + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">出口节点</span>' +
            '<span class="mono">' + (setupInfo.vpsHost ? E(setupInfo.vpsHost) : "未下发，配置时将跳过连通性校验") + "</span></div>" +
          (setupInfo.scriptReady
            ? ""
            : '<div class="hint-box" style="margin-top:10px">' + E(setupInfo.error || "未找到配置脚本") + "</div>") +
          '<div id="setupBar" style="display:none;margin-top:14px">' +
            '<div class="bar"><i style="width:0%"></i></div>' +
            '<div class="tiny muted" id="setupStep" style="margin-top:6px">准备中…</div>' +
          "</div>" +
          '<div class="log" id="setupLog" style="display:none;margin-top:12px;max-height:220px;overflow:auto"></div>' +
        "</div>" +
      "</section>" +

      // 资源
      '<section class="card">' +
        '<div class="card-head"><div>' +
          '<div class="card-title">资源配置</div>' +
          '<div class="card-sub">' + (running ? "工作站运行中，规格修改将在停止后可编辑" : "修改后于下次启动生效") + "</div>" +
        "</div></div>" +
        '<div class="card-body grid-3">' +
          '<div class="field"><label>名称</label><input class="input" id="ws-name" value="' + E(ws.name) + '" ' + (running ? "disabled" : "") + " /></div>" +
          '<div class="field"><label>CPU 核心</label><input class="input" id="ws-cpus" type="number" min="1" max="32" value="' + E(ws.cpus) + '" ' + (running ? "disabled" : "") + " /></div>" +
          '<div class="field"><label>内存（MB）</label><input class="input" id="ws-mem" type="number" min="512" step="512" value="' + E(ws.memoryMB) + '" ' + (running ? "disabled" : "") + " /></div>" +
          '<div class="field"><label>磁盘（GB）</label><input class="input" id="ws-disk" type="number" min="5" max="512" value="' + E(ws.diskGB) + '" ' + (running ? "disabled" : "") + " /></div>" +
          '<div class="field"><label>镜像</label><select class="input" id="ws-image" ' + (running ? "disabled" : "") + ">" +
            images.map(function (im) { return '<option value="' + E(im) + '"' + (im === ws.image ? " selected" : "") + ">" + E(im) + "</option>"; }).join("") +
          "</select></div>" +
          '<div class="field"><label>隔离驱动</label><input class="input" value="' + E(ws.driver) + '" disabled /></div>' +
        "</div>" +
        '<div class="form-actions"><button class="btn primary" data-act="save-specs" ' + (running ? "disabled" : "") + ">保存规格</button></div>" +
      "</section>" +

      // 引擎
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">隔离引擎</div>' +
          '<div class="card-sub">' + (engine.sandbox ? "策略隔离生效" : "策略隔离未生效，已回退受限进程模式") + "</div></div>" +
          '<span class="chip ' + (engine.sandbox ? "green" : "red") + '">' + (engine.sandbox ? "已启用" : "降级运行") + "</span></div>" +
        '<div class="card-body">' +
          (engine.sandbox ? "" : '<div class="hint-box" style="margin-bottom:12px">' + E(engine.sandboxError || "") + "</div>") +
          '<div class="grid-2">' +
            (engine.backends || []).map(function (b) {
              return '<div class="stat"><div class="k">' + E(b.kind === "vm" ? "虚拟机后端" : b.kind === "container" ? "容器后端" : "进程隔离") + "</div>" +
                '<div class="v" style="font-size:13px">' + E(b.name) + " " +
                '<span class="chip ' + (b.available ? "green" : "gray") + '">' + (b.available ? "可用" : "未安装") + "</span></div>" +
                '<div class="card-desc" style="font-size:11.5px">' + E(b.hint) + "</div></div>";
            }).join("") +
          "</div>" +
        "</div>" +
      "</section>" +

      // 系统环境（VPS 同源出入口）
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">系统环境</div>' +
          '<div class="card-sub">工作站启用后，终端 / 脚本 / 任务共用这套出入口</div></div>' +
          '<a class="btn small" href="#/env">修改</a></div>' +
        '<div class="card-body stats">' +
          '<div class="stat"><div class="k">语言 / 编码</div><div class="v" style="font-size:13px">' + E(sys.lang || "—") + "</div></div>" +
          '<div class="stat"><div class="k">时区</div><div class="v" style="font-size:13px">' + E(sys.tz || "—") + "</div></div>" +
          '<div class="stat"><div class="k">DNS</div><div class="v mono" style="font-size:12px">' +
            (dnsStats.running ? E(dnsStats.addr) : "系统解析器") + "</div></div>" +
          '<div class="stat"><div class="k">代理</div><div class="v mono" style="font-size:12px">' + E(sys.proxy || "未设置") + "</div></div>" +
        "</div>" +
      "</section>" +

      // 路径
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">工作站路径</div>' +
          '<div class="card-sub">所有隔离写入均被限制在该目录内</div></div>' +
          '<button class="btn" data-act="finder">' + A.icon("external", 14) + "在 Finder 中打开</button></div>" +
        '<div class="card-body">' +
          '<div class="list-row"><span class="tiny muted" style="width:96px">工作区</span><span class="mono wrap">' + E(ws.workspacePath || "") + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:96px">家目录</span><span class="mono wrap">' + E(ws.homePath || "") + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:96px">策略文件</span><span class="mono wrap">' + E(engine.policyPath || "") + "</span></div>" +
        "</div>" +
      "</section>";

    root.querySelector('[data-act="start"]')?.addEventListener("click", async function (e) {
      const btn = e.currentTarget;
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> 启动中';
      try {
        await A.api("/api/workstation/start", { method: "POST" });
        A.toast("工作站已启动", "ok");
      } catch (err) { A.toast(err.message, "err"); }
      A.refreshStatus();
      A.render();
    });

    root.querySelector('[data-act="stop"]')?.addEventListener("click", async function () {
      try {
        await A.api("/api/workstation/stop", { method: "POST" });
        A.toast("工作站已停止", "ok");
      } catch (err) { A.toast(err.message, "err"); }
      A.refreshStatus();
      A.render();
    });

    root.querySelector('[data-act="restart"]').addEventListener("click", async function (e) {
      const btn = e.currentTarget;
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> 重启中';
      try {
        await A.api("/api/workstation/restart", { method: "POST" });
        A.toast("工作站已重启", "ok");
      } catch (err) { A.toast(err.message, "err"); }
      A.refreshStatus();
      A.render();
    });

    // 一键配置环境：提交任务后通过 WS 跟踪进度
    root.querySelector('[data-act="setup"]')?.addEventListener("click", async function (e) {
      const btn = e.currentTarget;
      const bar = root.querySelector("#setupBar");
      const stepLabel = root.querySelector("#setupStep");
      const log = root.querySelector("#setupLog");
      const fill = bar.querySelector(".bar > i");

      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> 配置中';
      bar.style.display = "block";
      log.style.display = "block";
      log.textContent = "";
      fill.style.width = "0%";
      stepLabel.textContent = "正在启动配置任务…";

      function consume(chunk) {
        String(chunk).split("\n").forEach(function (line) {
          const raw = line.trim();
          const m = /^@@step (\d+)\/(\d+) (\S+) (\S+)/.exec(raw);
          if (m) {
            const done = Number(m[1]);
            const total = Math.max(1, Number(m[2]));
            fill.style.width = Math.min(100, Math.round((done / total) * 100)) + "%";
            stepLabel.textContent = "第 " + done + "/" + total + " 步 · " + m[3] + " · " + m[4];
            return;
          }
          if (raw) log.textContent += raw + "\n";
        });
        log.scrollTop = log.scrollHeight;
      }

      try {
        const res = await A.api("/api/setup/run", { method: "POST", body: { force: false } });
        let first = true;
        A.trackTask(res.taskId, function (chunk) {
          // snapshot 会重放完整历史，首包先清空避免重复
          if (first) { log.textContent = ""; first = false; }
          consume(chunk);
        }, function (task) {
          btn.disabled = false;
          btn.innerHTML = "一键配置环境";
          if (task && task.status === "succeeded") {
            fill.style.width = "100%";
            stepLabel.textContent = "环境已就绪";
            A.toast("环境配置完成", "ok");
          } else {
            const msg = (task && task.error) || "配置未完成";
            stepLabel.textContent = msg;
            A.toast(msg, "err");
          }
        });
      } catch (err) {
        btn.disabled = false;
        btn.innerHTML = "一键配置环境";
        stepLabel.textContent = "启动失败：" + err.message;
        A.toast(err.message, "err");
      }
    });

    root.querySelector('[data-act="save-specs"]')?.addEventListener("click", async function () {
      const patch = {
        name: root.querySelector("#ws-name").value,
        cpus: Number(root.querySelector("#ws-cpus").value),
        memoryMB: Number(root.querySelector("#ws-mem").value),
        diskGB: Number(root.querySelector("#ws-disk").value),
        image: root.querySelector("#ws-image").value,
      };
      try {
        await A.api("/api/workstation", { method: "PATCH", body: patch });
        A.toast("规格已保存", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="finder"]').addEventListener("click", async function () {
      try {
        await A.api("/api/maintenance/open-finder", { method: "POST" });
        A.toast("已在 Finder 中打开工作区", "ok");
      } catch (err) { A.toast(err.message, "err"); }
    });

    // ---- 工作站集合 ----
    root.querySelector('[data-act="ws-new"]')?.addEventListener("click", function () {
      const m = A.modal({
        title: "新建工作站",
        body:
          '<div class="grid-2">' +
            '<div class="field"><label>名称</label><input class="input" id="nw-name" placeholder="如 us-east" /></div>' +
            '<div class="field"><label>镜像</label><select class="input" id="nw-image">' +
              (images.length ? images : ["debian-12-arm64"]).map(function (im) {
                return '<option value="' + E(im) + '">' + E(im) + "</option>";
              }).join("") +
            "</select></div>" +
            '<div class="field"><label>CPU 核心</label><input class="input" id="nw-cpus" type="number" min="1" max="32" value="4" /></div>' +
            '<div class="field"><label>内存（MB）</label><input class="input" id="nw-mem" type="number" min="512" step="512" value="4096" /></div>' +
            '<div class="field"><label>磁盘（GB）</label><input class="input" id="nw-disk" type="number" min="5" max="512" value="20" /></div>' +
            '<div class="field"><label>语言</label><input class="input mono" id="nw-lang" value="' + E(ws.lang || "en_US.UTF-8") + '" /></div>' +
            '<div class="field"><label>时区</label><input class="input mono" id="nw-tz" value="' + E(ws.timeZone || "America/Los_Angeles") + '" /></div>' +
          "</div>" +
          '<label class="row tiny" style="gap:8px;margin-top:12px"><input type="checkbox" id="nw-net" /> 允许出网</label>' +
          '<label class="row tiny" style="gap:8px;margin-top:6px"><input type="checkbox" id="nw-vps" checked /> 出口走 VPS 隧道</label>',
        footer: '<button class="btn" data-c>取消</button><button class="btn primary" data-ok>创建</button>',
      });
      m.foot.querySelector("[data-c]").addEventListener("click", m.close);
      m.foot.querySelector("[data-ok]").addEventListener("click", async function () {
        const payload = {
          name: m.body.querySelector("#nw-name").value.trim(),
          image: m.body.querySelector("#nw-image").value,
          cpus: Number(m.body.querySelector("#nw-cpus").value),
          memoryMB: Number(m.body.querySelector("#nw-mem").value),
          diskGB: Number(m.body.querySelector("#nw-disk").value),
          lang: m.body.querySelector("#nw-lang").value.trim(),
          timeZone: m.body.querySelector("#nw-tz").value.trim(),
          network: m.body.querySelector("#nw-net").checked,
          useVps: m.body.querySelector("#nw-vps").checked,
        };
        if (!payload.name) { A.toast("请填写名称", "err"); return; }
        try {
          await A.api("/api/workstations", { method: "POST", body: payload });
          A.toast("已创建；切换后生效", "ok");
          m.close();
          A.refreshStatus();
          A.render();
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    root.querySelectorAll('[data-act="ws-activate"]').forEach(function (btn) {
      btn.addEventListener("click", async function () {
        btn.disabled = true;
        btn.innerHTML = '<span class="spinner"></span>';
        try {
          await A.api("/api/workstations/" + encodeURIComponent(btn.dataset.id) + "/activate", { method: "POST" });
          A.toast("已切换工作站", "ok");
          A.refreshStatus();
          A.render();
        } catch (err) { A.toast(err.message, "err"); btn.disabled = false; btn.textContent = "切换"; }
      });
    });

    root.querySelectorAll('[data-act="ws-delete"]').forEach(function (btn) {
      btn.addEventListener("click", async function () {
        const okDel = await A.confirm("删除工作站", "仅移除配置，工作区文件仍保留在磁盘上。", true);
        if (!okDel) return;
        try {
          await A.api("/api/workstations/" + encodeURIComponent(btn.dataset.id), { method: "DELETE" });
          A.toast("已删除", "ok");
          A.refreshStatus();
          A.render();
        } catch (err) { A.toast(err.message, "err"); }
      });
    });
  };
})();
