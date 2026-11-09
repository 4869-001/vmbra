/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  const LIC_STATUS = {
    active: { label: "已授权", color: "green" },
    grace: { label: "离线宽限", color: "amber" },
    expired: { label: "已过期", color: "red" },
    unlicensed: { label: "未激活", color: "gray" },
  };

  function licChip(status) {
    const m = LIC_STATUS[status] || LIC_STATUS.unlicensed;
    return '<span class="chip ' + m.color + '">' + m.label + "</span>";
  }

  function fmtDate(v) {
    if (!v) return "—";
    const d = new Date(v);
    if (isNaN(d.getTime())) return "—";
    const p = function (n) { return String(n).padStart(2, "0"); };
    return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) +
      " " + p(d.getHours()) + ":" + p(d.getMinutes());
  }

  A.views.settings = async function (root) {
    const results = await Promise.all([
      A.api("/api/settings"),
      A.api("/api/license/status"),
    ]);
    const data = results[0];
    const licData = results[1] || {};
    const lic = licData.license || {};
    const s = data.settings || {};
    const d = data.daemon || {};
    const storage = data.storage || {};
    const engine = data.engine || [];

    root.innerHTML =
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div><div class="card-title">常规</div>' +
            '<div class="card-sub">管理服务启动时自动启动工作站</div></div>' +
          '<button class="switch ' + (s.autoStart ? "on" : "") + '" data-toggle="auto"></button>' +
        "</div>" +
        '<div class="card-divider">' +
          '<div class="card-head">' +
            '<div><div class="card-title">运行脚本</div>' +
              '<div class="card-sub">在隔离环境内执行脚本，默认允许、无需二次确认；可在「应用」页随时运行。</div></div>' +
            '<span class="chip green">默认允许</span>' +
          "</div>" +
        "</div>" +
        '<div class="card-divider">' +
          '<div class="card-head">' +
            '<div><div class="card-title">外观</div><div class="card-sub">控制台主题</div></div>' +
            '<select class="input" id="theme" style="width:140px">' +
              '<option value="light"' + (s.theme !== "dark" ? " selected" : "") + ">浅色</option>" +
              '<option value="dark"' + (s.theme === "dark" ? " selected" : "") + ">深色</option>" +
            "</select>" +
          "</div>" +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">软件授权</div>' +
          '<div class="card-sub">首次激活会绑定本机设备指纹；更换设备需要在服务商后台解绑</div></div>' +
          licChip(lic.status) + "</div>" +
        '<div class="card-body">' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">套餐</span>' +
            '<span class="mono">' + E(lic.planLabel || "未激活") + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">授权码</span>' +
            '<span class="mono">' + E(lic.keyMasked || "—") + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">设备指纹</span>' +
            '<span class="mono">' + E(lic.fingerprint || "—") + "</span>" +
            '<span class="spacer"></span>' +
            '<button class="btn small" data-act="copy-fp">复制</button></div>' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">有效期至</span>' +
            '<span class="mono">' + (lic.plan === "lifetime" ? "永久" : fmtDate(lic.tokenExpire)) + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">上次校验</span>' +
            '<span class="mono">' + fmtDate(lic.lastOkAt) + "</span>" +
            '<span class="spacer"></span>' +
            '<button class="btn small" data-act="recheck">重新校验</button></div>' +
          (lic.lastError
            ? '<div class="hint-box" style="margin-top:10px">上次失败：' + E(lic.lastError) + "</div>"
            : "") +
          '<div class="field" style="margin-top:12px">' +
            '<div style="display:flex;gap:8px;align-items:center">' +
              '<input class="input" id="licKey" placeholder="VMB-XXXX-XXXX-XXXX-XXXX" autocomplete="off" spellcheck="false">' +
              '<button class="btn primary" data-act="activate" style="white-space:nowrap">激活</button>' +
            "</div>" +
          "</div>" +
          '<div class="hint-box" style="margin-top:10px">授权与出口节点绑定：激活成功后自动写入可用的 VPS 地址，「一键配置环境」会按该地址校验连通性。</div>' +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">配置导入</div>' +
          '<div class="card-sub">粘贴服务商发来的开通链接，按需勾选要下载的工具；也可以把当前配置导出成链接</div></div>' +
          '<span class="chip blue">vmbra://import</span></div>' +
        '<div class="card-body">' +
          '<textarea class="input" id="importLink" rows="3" spellcheck="false" placeholder="vmbra://import/…"></textarea>' +
          '<div style="display:flex;gap:8px;margin-top:10px;flex-wrap:wrap">' +
            '<button class="btn primary" data-act="import-open">导入并选择工具</button>' +
            '<button class="btn" data-act="import-export">复制当前配置链接</button>' +
          "</div>" +
          '<div class="hint-box" style="margin-top:10px">导入只写入配置与密钥；未勾选的工具不会下载。Linux 虚拟机终端（lima）需要额外勾选，约 1GB。</div>' +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">访问与安全</div>' +
          '<div class="card-sub">控制台仅监听回环地址，接口调用需要访问令牌</div></div></div>' +
        '<div class="card-body">' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">监听地址</span><span class="mono">' + E(d.listen) + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">访问令牌</span>' +
            '<span class="mono">••••••••' + E(d.token || "") + "</span>" +
            '<span class="spacer"></span>' +
            '<button class="btn small" data-act="rotate">轮换令牌</button></div>' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">令牌文件</span><span class="mono wrap">' + E((storage.dataDir || "") + "/token") + "</span></div>" +
          '<div class="hint-box" style="margin-top:10px">轮换后所有已打开的旧会话失效，浏览器会自动使用新令牌重新建立连接。</div>' +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">隔离后端</div>' +
          '<div class="card-sub">同一时间只会启用一个驱动，其余作为可选升级路径</div></div></div>' +
        '<div class="card-body">' +
          engine.map(function (b) {
            return '<div class="list-row">' +
              '<span class="mono" style="width:110px">' + E(b.name) + "</span>" +
              '<span class="tiny muted" style="flex:1">' + E(b.hint) + "</span>" +
              '<span class="chip ' + (b.available ? "green" : "gray") + '">' + (b.available ? "可用" : "未安装") + "</span>" +
            "</div>";
          }).join("") +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">关于</div>' +
          '<div class="card-sub">AnyWhere WorkStation · 本地隔离工作站</div></div>' +
          '<span class="chip blue">v' + E(d.version) + "</span></div>" +
        '<div class="card-body">' +
          '<div class="grid-2">' +
            '<div class="stat"><div class="k">服务进程</div><div class="v">pid ' + E(d.pid) + "</div></div>" +
            '<div class="stat"><div class="k">已运行</div><div class="v">' + E(d.uptime) + "</div></div>" +
            '<div class="stat"><div class="k">数据目录</div><div class="v" style="font-size:12px" >' + E(d.dataDir) + "</div></div>" +
            '<div class="stat"><div class="k">占用空间</div><div class="v">' + A.fmtBytes(storage.dataDirBytes) + "</div></div>" +
          "</div>" +
        "</div>" +
      "</section>";

    async function patch(body) {
      try {
        await A.api("/api/settings", { method: "PATCH", body: body });
        return true;
      } catch (err) {
        A.toast(err.message, "err");
        return false;
      }
    }

    root.querySelector('[data-toggle="auto"]').addEventListener("click", async function () {
      if (await patch({ autoStart: !s.autoStart })) {
        A.toast("设置已更新", "ok");
        A.render();
      }
    });

    root.querySelector("#theme").addEventListener("change", async function (e) {
      if (await patch({ theme: e.target.value })) {
        document.documentElement.setAttribute("data-theme", e.target.value);
        A.toast("主题已切换", "ok");
      }
    });

    root.querySelector('[data-act="rotate"]').addEventListener("click", async function () {
      const okGo = await A.confirm("轮换访问令牌", "轮换后旧的令牌立即失效，其他已打开的控制台需要重新连接。", true);
      if (!okGo) return;
      try {
        const res = await A.api("/api/settings/token/rotate", { method: "POST" });
        A.token = res.token;
        A.toast("令牌已轮换", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    // ---- 授权 ----
    const btnActivate = root.querySelector('[data-act="activate"]');
    if (btnActivate) {
      btnActivate.addEventListener("click", async function () {
        const input = root.querySelector("#licKey");
        const key = (input.value || "").trim();
        if (!key) { A.toast("请输入授权码", "err"); input.focus(); return; }

        btnActivate.disabled = true;
        const label = btnActivate.innerHTML;
        btnActivate.innerHTML = '<span class="spinner"></span> 激活中';
        try {
          await A.api("/api/license/activate", { method: "POST", body: { key: key } });
          A.toast("激活成功", "ok");
          A.render();
        } catch (err) {
          A.toast(err.message, "err");
          btnActivate.disabled = false;
          btnActivate.innerHTML = label;
        }
      });
    }

    const btnRecheck = root.querySelector('[data-act="recheck"]');
    if (btnRecheck) {
      btnRecheck.addEventListener("click", async function () {
        btnRecheck.disabled = true;
        try {
          await A.api("/api/license/heartbeat", { method: "POST" });
          A.toast("校验通过", "ok");
          A.render();
        } catch (err) {
          A.toast(err.message, "err");
          btnRecheck.disabled = false;
        }
      });
    }

    // ---- 配置导入 / 导出 ----
    root.querySelector('[data-act="import-open"]').addEventListener("click", function () {
      const input = root.querySelector("#importLink");
      const value = (input.value || "").trim();
      if (!value) {
        A.toast("请先粘贴开通链接", "err");
        input.focus();
        return;
      }
      A.openImport(value);
    });

    root.querySelector('[data-act="import-export"]').addEventListener("click", async function () {
      try {
        const res = await A.api("/api/import/link");
        root.querySelector("#importLink").value = res.link || "";
        await navigator.clipboard.writeText(res.link || "");
        A.toast("链接已复制（不含口令）", "ok");
      } catch (err) {
        A.toast(err.message, "err");
      }
    });

    const btnCopy = root.querySelector('[data-act="copy-fp"]');
    if (btnCopy) {
      btnCopy.addEventListener("click", function () {
        const fp = lic.fingerprintFull || lic.fingerprint || "";
        navigator.clipboard.writeText(fp).then(
          function () { A.toast("已复制设备指纹", "ok"); },
          function () { A.toast("复制失败", "err"); }
        );
      });
    }
  };
})();
