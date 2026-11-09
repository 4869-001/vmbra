/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  A.views.network = async function (root) {
    const [data, system, vpsInfo] = await Promise.all([
      A.api("/api/network"),
      A.api("/api/system"),
      A.api("/api/vps"),
    ]);
    const proxy = data.proxy || { mode: "system" };
    const forwards = data.forwards || [];
    const vpsCfg = vpsInfo.config || {};
    const vpsStatus = vpsInfo.status || {};
    const vpsForwards = vpsCfg.forwards || [];
    const vpsChip = vpsStatus.running
      ? '<span class="chip green">隧道运行中</span>'
      : vpsCfg.enabled
        ? '<span class="chip amber">已启用未连接</span>'
        : '<span class="chip gray">未启用</span>';
    const dns = system.dns || { mode: "system", servers: [], search: [], port: 1053, enabled: true };
    const dnsStats = system.dnsStats || {};
    const recent = system.dnsRecent || [];
    const dnsChip = system.dnsError
      ? '<span class="chip red">启动失败</span>'
      : dnsStats.running
        ? '<span class="chip green">转发器 ' + E(dnsStats.addr) + "</span>"
        : '<span class="chip gray">已关闭</span>';

    root.innerHTML =
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div style="max-width:620px">' +
            '<div class="card-title">工作站出网</div>' +
            '<div class="card-desc">关闭时，隔离环境内的进程无法建立任何对外连接（默认关闭）。</div>' +
          "</div>" +
          '<div class="row"><span class="chip ' + (data.network ? "green" : "gray") + '">' + (data.network ? "已允许" : "已禁用") + "</span>" +
          '<button class="switch ' + (data.network ? "on" : "") + '" data-toggle="net"></button></div>' +
        "</div>" +
        '<div class="card-divider"><div class="tiny muted">' + E(data.note) + "</div></div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">代理</div>' +
          '<div class="card-sub">仅在工作站出网开启时生效</div></div></div>' +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>模式</label><select class="input" id="proxy-mode">' +
              '<option value="off"' + (proxy.mode === "off" ? " selected" : "") + ">不使用代理</option>" +
              '<option value="system"' + (proxy.mode === "system" ? " selected" : "") + ">继承宿主机环境变量</option>" +
              '<option value="manual"' + (proxy.mode === "manual" ? " selected" : "") + ">手动配置</option>" +
            "</select></div>" +
            '<div class="field"><label>HTTP 代理</label><input class="input" id="proxy-http" value="' + E(proxy.http || "") + '" placeholder="http://127.0.0.1:7890" /></div>' +
            '<div class="field"><label>HTTPS 代理</label><input class="input" id="proxy-https" value="' + E(proxy.https || "") + '" placeholder="http://127.0.0.1:7890" /></div>' +
            '<div class="field"><label>NO_PROXY</label><input class="input" id="proxy-no" value="' + E(proxy.noProxy || "") + '" placeholder="localhost,127.0.0.1,.local" /></div>' +
          "</div>" +
          '<div class="form-actions"><button class="btn primary" data-act="save-proxy">保存代理</button></div>' +
        "</div>" +
      "</section>" +

      // VPS 出入口
      '<section class="card">' +
        '<div class="card-head">' +
          '<div style="max-width:620px"><div class="card-title">VPS 出入口</div>' +
            '<div class="card-desc">' + E(vpsInfo.hint || "") + "</div></div>" +
          vpsChip +
        "</div>" +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>VPS 主机</label><input class="input mono" id="vps-host" value="' + E(vpsCfg.host || "") + '" placeholder="38.58.108.195" /></div>' +
            '<div class="field"><label>SSH 端口</label><input class="input" id="vps-port" type="number" min="1" max="65535" value="' + E(vpsCfg.port || 22) + '" /></div>' +
            '<div class="field"><label>登录用户</label><input class="input mono" id="vps-user" value="' + E(vpsCfg.user || "root") + '" /></div>' +
            '<div class="field"><label>SOCKS5 端口（本机）</label><input class="input" id="vps-socks" type="number" min="1024" max="65535" value="' + E(vpsCfg.socksPort || 1080) + '" /></div>' +
            '<div class="field"><label>私钥路径</label><input class="input mono" id="vps-key" value="' + E(vpsInfo.keyPath || "") + '" /></div>' +
            '<div class="field"><label>远端绑定</label><label class="row tiny" style="height:32px;gap:8px">' +
              '<button class="switch ' + (vpsCfg.gatewayPorts ? "on" : "") + '" data-toggle="vps-gateway"></button>' +
              '<span class="muted">' + (vpsCfg.gatewayPorts ? "0.0.0.0（需 GatewayPorts）" : "仅 VPS 回环") + "</span></label></div>" +
          "</div>" +
          '<div class="form-actions">' +
            '<button class="btn" data-act="vps-key">' + A.icon("file", 13) + "生成/查看公钥</button>" +
            '<button class="btn" data-act="vps-test">测试 SSH</button>' +
            '<button class="btn" data-act="vps-save">保存配置</button>' +
            (vpsStatus.running
              ? '<button class="btn danger" data-act="vps-stop">停止隧道</button>'
              : '<button class="btn primary" data-act="vps-start">' + A.icon("play", 13) + "启动隧道</button>") +
          "</div>" +
          (vpsInfo.keyReady
            ? '<div class="hint-box" style="margin-top:10px">公钥已生成。若 VPS 尚未信任它，点「生成/查看公钥」复制安装命令，在 Mac 终端执行一次即可。</div>'
            : '<div class="hint-box" style="margin-top:10px">还没有专用密钥：先点「生成/查看公钥」，把公钥装到 VPS 后再启动。</div>') +
          (vpsStatus.error ? '<div class="hint-box" style="margin-top:10px">' + E(vpsStatus.error) + "</div>" : "") +

          '<div class="card-divider">' +
            '<div class="card-title" style="font-size:13px">入站转发（VPS 端口 → 工作站端口）</div>' +
            (vpsForwards.length
              ? '<table class="table"><thead><tr><th>说明</th><th>VPS 端口</th><th>工作站端口</th><th>状态</th><th style="width:60px"></th></tr></thead><tbody>' +
                vpsForwards.map(function (f) {
                  const chip = !f.enabled
                    ? '<span class="chip gray">停用</span>'
                    : vpsStatus.running
                      ? '<span class="chip green">已转发</span>'
                      : '<span class="chip amber">待启动</span>';
                  return "<tr>" +
                    "<td>" + E(f.label || "VPS " + f.remotePort) + "</td>" +
                    '<td class="mono">' + f.remotePort + "</td>" +
                    '<td class="mono">' + f.localPort + "</td>" +
                    "<td>" + chip + "</td>" +
                    '<td class="actions"><button class="btn small ghost" data-vpsfw-del="' + E(f.id) + '">' + A.icon("trash", 13) + "</button></td>" +
                  "</tr>";
                }).join("") + "</tbody></table>"
              : '<div class="empty">暂无入站规则</div>') +
            '<div class="grid-3">' +
              '<div class="field"><label>说明</label><input class="input" id="vps-fw-label" placeholder="例如 Web 预览" /></div>' +
              '<div class="field"><label>VPS 端口</label><input class="input" id="vps-fw-remote" type="number" min="1" max="65535" placeholder="8080" /></div>' +
              '<div class="field"><label>工作站端口</label><input class="input" id="vps-fw-local" type="number" min="1" max="65535" placeholder="8080" /></div>' +
            "</div>" +
            '<div class="form-actions">' +
              '<span class="tiny muted" style="margin-right:auto">VPS 端口需在安全组/防火墙放行；绑定 0.0.0.0 需 sshd 的 GatewayPorts。</span>' +
              '<button class="btn" data-act="vps-add-fw">' + A.icon("plus", 13) + "添加入站</button>" +
            "</div>" +
          "</div>" +

          '<div class="card-divider">' +
            '<div class="row">' +
              '<button class="btn" data-act="vps-egress" ' + (vpsStatus.running ? "" : "disabled") + ">对比出口 IP</button>" +
              '<span class="tiny muted" id="vps-egress-out">点击按钮对比宿主与 VPS 的出口 IP</span>' +
            "</div>" +
          "</div>" +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">端口转发</div>' +
          '<div class="card-sub">把主机端口映射到工作站内部端口</div></div></div>' +
        '<div class="card-body">' +
          (forwards.length
            ? '<table class="table"><thead><tr><th>说明</th><th>主机端口</th><th>工作站端口</th><th>运行状态</th><th style="width:180px"></th></tr></thead><tbody>' +
              forwards.map(function (f) {
                const rt = f.runtime || {};
                let chip = '<span class="chip gray">已停用</span>';
                if (f.enabled && rt.listening) chip = '<span class="chip green">监听中</span>';
                else if (f.enabled && rt.error) chip = '<span class="chip red">端口被占用</span>';
                else if (f.enabled) chip = '<span class="chip amber">工作站未运行</span>';
                const traffic = rt.listening && rt.connections
                  ? '<div class="tiny muted" style="margin-top:3px">' + rt.connections + " 次连接 · ↑" + A.fmtBytes(rt.bytesUp) + " ↓" + A.fmtBytes(rt.bytesDown) + "</div>"
                  : "";
                return "<tr>" +
                  "<td>" + E(f.label) +
                    '<div class="tiny muted mono">127.0.0.1:' + f.hostPort + " → 127.0.0.1:" + f.guestPort + "</div></td>" +
                  '<td class="mono">' + f.hostPort + "</td>" +
                  '<td class="mono">' + f.guestPort + "</td>" +
                  "<td>" + chip + traffic + "</td>" +
                  '<td class="actions">' +
                    '<button class="btn small ghost" data-fw-test="' + E(f.id) + '">测试</button>' +
                    '<button class="switch ' + (f.enabled ? "on" : "") + '" data-fw-toggle="' + E(f.id) + '"></button>' +
                    '<button class="btn small ghost" data-fw-del="' + E(f.id) + '">' + A.icon("trash", 13) + "</button>" +
                  "</td>" +
                "</tr>";
              }).join("") +
              "</tbody></table>"
            : '<div class="empty">暂无转发规则</div>') +
          '<div class="card-divider">' +
            '<div class="grid-3">' +
              '<div class="field"><label>说明</label><input class="input" id="fw-label" placeholder="例如 本地预览" /></div>' +
              '<div class="field"><label>主机端口</label><input class="input" id="fw-host" type="number" min="1" max="65535" placeholder="8080" /></div>' +
              '<div class="field"><label>工作站端口</label><input class="input" id="fw-guest" type="number" min="1" max="65535" placeholder="80" /></div>' +
            "</div>" +
            '<div class="form-actions">' +
              '<span class="tiny muted" style="margin-right:auto">协议：TCP（本地进程隔离不支持 UDP 转发）</span>' +
              '<button class="btn primary" data-act="add-fw">' + A.icon("plus", 13) + "添加规则</button>" +
            "</div>" +
          "</div>" +
        "</div>" +
      "</section>" +

      // DNS
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">DNS 解析</div>' +
          '<div class="card-sub">终端、脚本与任务共用同一条解析出口；自定义上游由本地转发器统一转发。</div></div>' +
          dnsChip + "</div>" +
        (system.dnsError ? '<div class="hint-box" style="margin:10px 0 0">' + E(system.dnsError) + "</div>" : "") +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>模式</label><select class="input" id="dns-mode">' +
              '<option value="system"' + (dns.mode === "system" ? " selected" : "") + ">系统解析器（与宿主机一致）</option>" +
              '<option value="custom"' + (dns.mode === "custom" ? " selected" : "") + ">自定义上游（本地转发）</option>" +
            "</select></div>" +
            '<div class="field"><label>上游服务器（逗号分隔）</label><input class="input mono" id="dns-servers" value="' +
              E((dns.servers || []).join(", ")) + '" placeholder="223.5.5.5, 1.1.1.1" /></div>' +
            '<div class="field"><label>转发器端口</label><input class="input" id="dns-port" type="number" min="1024" max="65535" value="' +
              E(dns.port) + '" /></div>' +
            '<div class="field"><label>搜索域（逗号分隔）</label><input class="input mono" id="dns-search" value="' +
              E((dns.search || []).join(", ")) + '" placeholder="corp.local" /></div>' +
            '<div class="field"><label>宿主机 DNS</label><input class="input mono" value="' +
              E((system.hostDNS || []).join(", ") || "未知") + '" disabled /></div>' +
            '<div class="field"><label>本地转发器</label><label class="row tiny" style="height:32px;gap:8px">' +
              '<button class="switch ' + (dns.enabled ? "on" : "") + '" data-toggle="dns"></button>' +
              "<span class=\"muted\">" + (dns.enabled ? "启用" : "停用") + "</span></label></div>" +
          "</div>" +
          '<div class="form-actions"><button class="btn primary" data-act="save-dns">保存并应用</button></div>' +
          '<div class="card-divider">' +
            '<div class="row">' +
              '<input class="input mono" id="dns-test-name" placeholder="要解析的域名" value="example.com" style="width:240px" />' +
              '<button class="btn" data-act="dns-test">解析测试</button>' +
              '<span class="tiny muted" id="dns-test-state"></span>' +
            "</div>" +
            '<div id="dns-test-out" class="card-body" style="margin-top:10px"></div>' +
          "</div>" +
          (recent.length
            ? '<div class="card-divider"><div class="tiny muted" style="margin-bottom:6px">最近查询</div>' +
              '<table class="table"><tbody>' +
              recent.slice(0, 6).map(function (q) {
                return "<tr><td>" + E(q.name || "—") + '</td><td class="muted mono">' + E(q.upstream) + "</td>" +
                  '<td class="muted">' + q.ms + "ms</td>" +
                  "<td>" + (q.ok ? '<span class="chip green">成功</span>' : '<span class="chip red">失败</span>') + "</td></tr>";
              }).join("") +
              "</tbody></table></div>"
            : "") +
        "</div>" +
      "</section>" +

      // 出口探测
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">出口探测</div>' +
          '<div class="card-sub">在隔离环境内发起请求，确认实际出口 IP、解析器与代理链路</div></div>' +
          '<button class="btn" data-act="egress" ' + (data.network ? "" : "disabled") + ">探测出口</button></div>" +
        '<div class="card-body" id="egress-out">' +
          (data.network
            ? '<div class="tiny muted">点击「探测出口」，通过 cloudflare trace / ipify 获取出口信息。</div>'
            : '<div class="tiny muted">工作站出网已关闭，需要先在上方开启。</div>') +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">连通性测试</div>' +
          '<div class="card-sub">从宿主机发起 TCP 探测</div></div></div>' +
        '<div class="card-body">' +
          '<div class="row">' +
            '<input class="input" id="test-host" placeholder="127.0.0.1" style="width:220px" value="127.0.0.1" />' +
            '<input class="input" id="test-port" type="number" placeholder="端口" style="width:120px" />' +
            '<button class="btn" data-act="test">测试</button>' +
            '<span class="tiny muted" id="test-result"></span>' +
          "</div>" +
        "</div>" +
      "</section>";

    root.querySelector('[data-toggle="net"]').addEventListener("click", async function () {
      try {
        await A.api("/api/network/toggle", { method: "PUT", body: { enabled: !data.network } });
        A.toast(!data.network ? "工作站已允许出网" : "工作站已禁止出网", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="save-proxy"]').addEventListener("click", async function () {
      const body = {
        mode: root.querySelector("#proxy-mode").value,
        http: root.querySelector("#proxy-http").value,
        https: root.querySelector("#proxy-https").value,
        noProxy: root.querySelector("#proxy-no").value,
      };
      try {
        await A.api("/api/network/proxy", { method: "PUT", body: body });
        A.toast("代理配置已保存", "ok");
      } catch (err) { A.toast(err.message, "err"); }
    });

    // ---- VPS 出入口 ----
    function vpsFormBody() {
      return {
        host: root.querySelector("#vps-host").value.trim(),
        port: Number(root.querySelector("#vps-port").value) || 22,
        user: root.querySelector("#vps-user").value.trim(),
        socksPort: Number(root.querySelector("#vps-socks").value) || 1080,
        keyPath: root.querySelector("#vps-key").value.trim(),
        gatewayPorts: !!vpsCfg.gatewayPorts,
      };
    }

    async function saveVPS(extra) {
      try {
        await A.api("/api/vps", { method: "PUT", body: Object.assign(vpsFormBody(), extra || {}) });
        return true;
      } catch (err) {
        A.toast(err.message, "err");
        return false;
      }
    }

    root.querySelector('[data-toggle="vps-gateway"]').addEventListener("click", async function () {
      if (await saveVPS({ gatewayPorts: !vpsCfg.gatewayPorts })) A.render();
    });

    root.querySelector('[data-act="vps-save"]').addEventListener("click", async function () {
      if (await saveVPS()) {
        A.toast("VPS 配置已保存", "ok");
        A.render();
      }
    });

    root.querySelector('[data-act="vps-key"]').addEventListener("click", async function () {
      try {
        const res = await A.api("/api/vps/key", { method: "POST" });
        const pub = res.publicKey || "";
        const cmd = (res.info && res.info.installCommand) || "";
        const m = A.modal({
          title: "VPS 公钥",
          body:
            '<div class="card-desc" style="margin:0 0 10px">把公钥追加到 VPS 的 <span class="mono">~/.ssh/authorized_keys</span>；也可以在 Mac 终端执行下面的命令（会提示输入 VPS 密码）。</div>' +
            '<div class="field"><label>公钥</label><textarea class="input mono" id="vps-pub" style="height:72px" readonly>' + E(pub) + "</textarea></div>" +
            '<div class="field"><label>安装命令</label><textarea class="input mono" id="vps-install" style="height:88px" readonly>' + E(cmd) + "</textarea></div>",
          footer: '<button class="btn" data-c>关闭</button><button class="btn primary" data-copy>复制命令</button>',
        });
        m.foot.querySelector("[data-c]").addEventListener("click", m.close);
        m.foot.querySelector("[data-copy]").addEventListener("click", async function () {
          try {
            await navigator.clipboard.writeText(cmd || pub);
            A.toast("已复制安装命令", "ok");
          } catch (e) {
            A.toast("复制失败，请手动选中文本", "err");
          }
        });
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="vps-test"]').addEventListener("click", async function () {
      const btn = this;
      btn.disabled = true;
      try {
        if (await saveVPS()) {
          const res = await A.api("/api/vps/test", { method: "POST" });
          if (res.ok) A.toast("SSH 登录成功，可以启动隧道", "ok");
          else A.toast("SSH 登录失败：" + (res.error || res.output || "未知原因"), "err");
        }
      } catch (err) { A.toast(err.message, "err"); }
      btn.disabled = false;
    });

    root.querySelector('[data-act="vps-start"]')?.addEventListener("click", async function () {
      const btn = this;
      btn.disabled = true;
      try {
        if (await saveVPS()) {
          if (!data.network) {
            await A.api("/api/network/toggle", { method: "PUT", body: { enabled: true } });
          }
          await A.api("/api/vps/start", { method: "POST" });
          A.toast("VPS 隧道已建立，出口切到 VPS", "ok");
          A.render();
        }
      } catch (err) { A.toast(err.message, "err"); }
      btn.disabled = false;
    });

    root.querySelector('[data-act="vps-stop"]')?.addEventListener("click", async function () {
      try {
        await A.api("/api/vps/stop", { method: "POST" });
        A.toast("隧道已停止，出口恢复为宿主", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="vps-add-fw"]').addEventListener("click", async function () {
      const remote = Number(root.querySelector("#vps-fw-remote").value);
      const local = Number(root.querySelector("#vps-fw-local").value);
      if (!remote || !local) { A.toast("请填写端口", "err"); return; }
      const next = vpsForwards.map(function (f) {
        return { id: f.id, label: f.label, remotePort: f.remotePort, localPort: f.localPort, enabled: f.enabled };
      });
      next.push({
        label: root.querySelector("#vps-fw-label").value.trim(),
        remotePort: remote,
        localPort: local,
        enabled: true,
      });
      if (await saveVPS({ forwards: next })) {
        A.toast("入站规则已添加，重启隧道后生效", "ok");
        A.render();
      }
    });

    root.querySelectorAll("[data-vpsfw-del]").forEach(function (el) {
      el.addEventListener("click", async function () {
        const next = vpsForwards
          .filter(function (f) { return f.id !== el.dataset.vpsfwDel; })
          .map(function (f) { return { id: f.id, label: f.label, remotePort: f.remotePort, localPort: f.localPort, enabled: f.enabled }; });
        if (await saveVPS({ forwards: next })) A.render();
      });
    });

    root.querySelector('[data-act="vps-egress"]')?.addEventListener("click", async function () {
      const out = root.querySelector("#vps-egress-out");
      out.textContent = "探测中…";
      try {
        const res = await A.api("/api/vps/egress", { method: "POST" });
        if (res.error) {
          out.textContent = "探测失败：" + res.error;
          return;
        }
        out.textContent = "宿主 " + (res.hostIP || "未知") + " → VPS " + (res.vpsIP || "未知") +
          (res.effective ? "（隧道生效）" : "（未生效）");
      } catch (err) {
        out.textContent = err.message;
      }
    });

    root.querySelectorAll("[data-fw-toggle]").forEach(function (el) {
      el.addEventListener("click", async function () {
        const on = !el.classList.contains("on");
        try {
          await A.api("/api/network/forwards/" + el.dataset.fwToggle, { method: "PATCH", body: { enabled: on } });
          A.render();
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    root.querySelectorAll("[data-fw-test]").forEach(function (el) {
      el.addEventListener("click", async function () {
        el.disabled = true;
        try {
          const res = await A.api("/api/network/forwards/" + el.dataset.fwTest + "/test", { method: "POST" });
          if (!res.listening) {
            A.toast("转发未运行：" + (res.error || "工作站未启动"), "err");
          } else if (!res.guestOk) {
            A.toast("端口已监听，但" + (res.guestError || "工作站端口不可达"), "err");
          } else {
            A.toast("链路正常 · " + res.addr + " · 目标 " + res.guestLatencyMs + "ms", "ok");
          }
        } catch (err) {
          A.toast(err.message, "err");
        }
        el.disabled = false;
      });
    });

    root.querySelectorAll("[data-fw-del]").forEach(function (el) {
      el.addEventListener("click", async function () {
        try {
          await A.api("/api/network/forwards/" + el.dataset.fwDel, { method: "DELETE" });
          A.toast("规则已删除", "ok");
          A.render();
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    root.querySelector('[data-act="add-fw"]').addEventListener("click", async function () {
      const body = {
        label: root.querySelector("#fw-label").value,
        hostPort: Number(root.querySelector("#fw-host").value),
        guestPort: Number(root.querySelector("#fw-guest").value),
        proto: "tcp",
      };
      if (!body.hostPort || !body.guestPort) { A.toast("请填写端口", "err"); return; }
      try {
        await A.api("/api/network/forwards", { method: "POST", body: body });
        A.toast("规则已添加", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="test"]').addEventListener("click", async function () {
      const el = root.querySelector("#test-result");
      el.textContent = "测试中…";
      try {
        const res = await A.api("/api/network/test", {
          method: "POST",
          body: {
            host: root.querySelector("#test-host").value,
            port: Number(root.querySelector("#test-port").value),
          },
        });
        el.textContent = res.ok ? "可达，延迟 " + res.latencyMs + "ms" : "不可达：" + res.error;
        el.style.color = res.ok ? "#128a4b" : "#c22e35";
      } catch (err) {
        el.textContent = err.message;
      }
    });

    // ---- DNS ----
    function splitList(v) {
      return v.split(/[,\s]+/).map(function (s) { return s.trim(); }).filter(Boolean);
    }

    root.querySelector('[data-toggle="dns"]').addEventListener("click", async function () {
      try {
        const res = await A.api("/api/system/dns", { method: "PATCH", body: { enabled: !dns.enabled } });
        A.toast(res.dns.enabled ? "DNS 转发器已启用" : "DNS 转发器已停用", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="save-dns"]').addEventListener("click", async function () {
      try {
        await A.api("/api/system/dns", {
          method: "PATCH",
          body: {
            mode: root.querySelector("#dns-mode").value,
            servers: splitList(root.querySelector("#dns-servers").value),
            search: splitList(root.querySelector("#dns-search").value),
            port: Number(root.querySelector("#dns-port").value),
          },
        });
        A.toast("DNS 配置已应用", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="dns-test"]').addEventListener("click", async function () {
      const out = root.querySelector("#dns-test-out");
      const stateEl = root.querySelector("#dns-test-state");
      const name = root.querySelector("#dns-test-name").value.trim();
      stateEl.textContent = "解析中…";
      try {
        const res = await A.api("/api/system/dns/test", { method: "POST", body: { name: name, timeoutSec: 4 } });
        stateEl.textContent = "";
        out.innerHTML = '<table class="table"><thead><tr><th>解析器</th><th>结果</th><th>耗时</th></tr></thead><tbody>' +
          res.results.map(function (r) {
            return "<tr><td>" + E(r.resolver) + "</td>" +
              "<td>" + (r.ok
                ? '<span class="mono tiny">' + E((r.addresses || []).join(", ")) + "</span>"
                : '<span class="tiny" style="color:#c22e35">' + E(r.error || "失败") + "</span>") + "</td>" +
              '<td class="muted">' + r.ms + "ms</td></tr>";
          }).join("") + "</tbody></table>";
      } catch (err) {
        stateEl.textContent = "";
        out.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>";
      }
    });

    // ---- 出口探测 ----
    root.querySelector('[data-act="egress"]')?.addEventListener("click", async function (e) {
      const btn = e.currentTarget;
      const out = root.querySelector("#egress-out");
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> 探测中';
      out.innerHTML = '<div class="loading-row"><span class="spinner"></span> 正在从隔离环境发起请求…</div>';
      try {
        const res = await A.api("/api/network/egress", { method: "POST" });
        out.innerHTML =
          '<div class="stats">' +
            '<div class="stat"><div class="k">出口 IP</div><div class="v mono">' + E(res.ip || "—") + "</div></div>" +
            '<div class="stat"><div class="k">地区</div><div class="v">' + E(res.location || "—") + "</div></div>" +
            '<div class="stat"><div class="k">延迟</div><div class="v">' + res.latencyMs + " ms</div></div>" +
            '<div class="stat"><div class="k">DNS</div><div class="v mono" style="font-size:12px">' + E(res.dnsServer || "系统解析器") + "</div></div>" +
            '<div class="stat"><div class="k">代理</div><div class="v mono" style="font-size:12px">' + E(res.proxy || "直连") + "</div></div>" +
          "</div>" +
          (res.error ? '<div class="hint-box" style="margin-top:10px">' + E(res.error) + "</div>" : "");
      } catch (err) {
        out.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>";
      }
      btn.disabled = false;
      btn.textContent = "探测出口";
    });
  };
})();
