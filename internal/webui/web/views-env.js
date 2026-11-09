/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  A.views.env = async function (root) {
    const [env, desktopStatus, system, vpsInfo] = await Promise.all([
      A.api("/api/env"),
      A.api("/api/status").then((s) => s.desktop),
      A.api("/api/system"),
      A.api("/api/vps"),
    ]);
    const vpsRegion = (vpsInfo && vpsInfo.config) || {};
    const vars = env.vars || [];
    const mounts = env.mounts || [];
    const desktop = env.desktop || {};
    const policy = env.policy || {};
    const locale = system.locale || {};
    const locales = system.locales || ["zh_CN.UTF-8", "en_US.UTF-8"];
    const envPreview = (system.env || []).filter(function (kv) {
      return ["LANG", "LC_ALL", "LC_CTYPE", "TZ", "TERM", "COLORTERM", "ANYWHERE_DNS_SERVER", "HTTPS_PROXY", "TERM_PROGRAM"].indexOf(kv.key) >= 0;
    });

    root.innerHTML =
      // 系统语言文字
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">系统语言文字</div>' +
          '<div class="card-sub">终端、脚本与任务共用这套环境，与宿主 Mac 无关' +
            (locale.followVps ? "；当前跟随 VPS 地区" : "") + '</div></div>' +
          '<span class="chip ' + (/UTF-8/i.test(locale.encoding || "") ? "green" : "amber") + '">' + E(locale.encoding || "UTF-8") + "</span></div>" +
        '<div class="card-body">' +
          '<div class="card-head" style="margin-bottom:14px">' +
            '<div><div class="card-title" style="font-size:13px">跟随 VPS 地区</div>' +
              '<div class="card-desc">按 VPS 所在地区设置语言与时区（当前 ' +
                E((vpsRegion.lang || "en-US") + " · " + (vpsRegion.timeZone || "America/Los_Angeles")) +
                "），而不是沿用宿主设置</div></div>" +
            '<button class="switch ' + (locale.followVps ? "on" : "") + '" data-toggle="locale-vps" aria-label="跟随 VPS 地区"></button>' +
          "</div>" +
          '<div class="grid-3">' +
            '<div class="field"><label>语言 / 文字</label><select class="input" id="locale-lang"' +
              (locale.followVps ? " disabled" : "") + ">" +
              locales.map(function (l) {
                return '<option value="' + E(l) + '"' + (l === locale.lang ? " selected" : "") + ">" + E(l) + "</option>";
              }).join("") +
              (locales.indexOf(locale.lang) < 0 && locale.lang
                ? '<option value="' + E(locale.lang) + '" selected>' + E(locale.lang) + "</option>"
                : "") +
            "</select></div>" +
            '<div class="field"><label>编码</label><input class="input" value="' + E(locale.encoding || "UTF-8") + '" disabled /></div>' +
            '<div class="field"><label>时区</label><input class="input mono" id="locale-tz" list="tz-list" value="' + E(locale.timezone || "") + '"' +
              (locale.followVps ? " disabled" : "") + " />" +
              '<datalist id="tz-list">' +
                ["Asia/Shanghai", "Asia/Taipei", "Asia/Tokyo", "Asia/Singapore", "UTC", "Europe/London", "America/Los_Angeles", "America/New_York"]
                  .map(function (z) { return '<option value="' + z + '"></option>'; }).join("") +
              "</datalist></div>" +
          "</div>" +
          '<div class="form-actions">' +
            '<span class="tiny muted" style="margin-right:auto">统一环境文件：<span class="mono">' + E(system.shellEnvPath || "") + "</span></span>" +
            '<button class="btn primary" data-act="save-locale">保存并应用</button>' +
          "</div>" +
          '<div class="card-divider">' +
            '<div class="tiny muted" style="margin-bottom:6px">终端环境预览</div>' +
            '<table class="table"><tbody>' +
              envPreview.map(function (kv) {
                return '<tr><td class="mono" style="width:220px">' + E(kv.key) + '</td><td class="mono muted">' +
                  E(kv.value.length > 90 ? kv.value.slice(0, 90) + "…" : kv.value) + "</td></tr>";
              }).join("") +
            "</tbody></table>" +
          "</div>" +
        "</div>" +
      "</section>" +
      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">环境变量</div>' +
          '<div class="card-sub">注入到工作站的终端与命令会话</div></div>' +
          '<button class="btn small" data-act="add-var">' + A.icon("plus", 13) + "添加</button></div>" +
        '<div class="card-body" id="var-list">' +
          (vars.length
            ? vars.map(function (v, i) {
                return '<div class="row" data-var="' + i + '" style="margin-bottom:8px">' +
                  '<input class="input mono" data-k style="width:220px" value="' + E(v.key) + '" placeholder="KEY" />' +
                  '<span class="muted mono">=</span>' +
                  '<input class="input mono" data-v style="flex:1" value="' + E(v.value) + '" placeholder="value" />' +
                  '<button class="btn small ghost" data-del-var>' + A.icon("trash", 13) + "</button>" +
                "</div>";
              }).join("")
            : '<div class="empty">暂无环境变量</div>') +
        "</div>" +
        '<div class="form-actions"><button class="btn primary" data-act="save-vars">保存变量</button></div>' +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">挂载目录</div>' +
          '<div class="card-sub">把宿主目录只读共享给工作站（写入仍限制在工作区内）</div></div>' +
          '<button class="btn small" data-act="add-mount">' + A.icon("plus", 13) + "添加</button></div>" +
        '<div class="card-body" id="mount-list">' +
          (mounts.length
            ? mounts.map(function (m, i) {
                return '<div class="row" data-mount="' + i + '" style="margin-bottom:8px">' +
                  '<input class="input mono" data-m style="flex:1" value="' + E(m) + '" placeholder="/Users/you/Projects" />' +
                  '<button class="btn small ghost" data-del-mount>' + A.icon("trash", 13) + "</button>" +
                "</div>";
              }).join("")
            : '<div class="empty">暂无挂载目录</div>') +
        "</div>" +
        '<div class="form-actions"><button class="btn primary" data-act="save-mounts">保存挂载</button></div>' +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">图形桌面接入</div>' +
          '<div class="card-sub">接入工作站内的 VNC 服务（例如 Lima + TigerVNC）</div></div>' +
          '<span class="chip ' + (desktopStatus && desktopStatus.available ? "green" : "gray") + '">' +
            (desktopStatus && desktopStatus.available ? "VNC 可达" : "未连接") + "</span></div>" +
        '<div class="card-body">' +
          '<div class="grid-3">' +
            '<div class="field"><label>VNC 主机</label><input class="input" id="vnc-host" value="' + E(desktop.vncHost || "") + '" placeholder="127.0.0.1" /></div>' +
            '<div class="field"><label>端口</label><input class="input" id="vnc-port" type="number" value="' + E(desktop.vncPort || 5900) + '" /></div>' +
            '<div class="field"><label>密码（可选）</label><input class="input" id="vnc-pass" value="' + E(desktop.password || "") + '" /></div>' +
            '<div class="field"><label>缩放</label><select class="input" id="vnc-scale">' +
              '<option value="fit"' + (desktop.scale !== "100" ? " selected" : "") + ">自适应窗口</option>" +
              '<option value="100"' + (desktop.scale === "100" ? " selected" : "") + ">100% 原始尺寸</option>" +
            "</select></div>" +
          "</div>" +
          (desktopStatus && desktopStatus.reason
            ? '<div class="hint-box" style="margin-top:12px">' + E(desktopStatus.reason) + "</div>"
            : "") +
          '<div class="form-actions"><button class="btn primary" data-act="save-desktop">保存并检测</button></div>' +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">隔离与运行时</div>' +
          '<div class="card-sub">策略文件由管理服务自动生成，修改配置后即时重建</div></div></div>' +
        '<div class="card-body">' +
          '<div class="list-row"><span class="tiny muted" style="width:110px">镜像</span><span class="mono">' + E(env.workstation.image) + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">规格</span><span class="mono">' +
            E(env.workstation.cpus) + " vCPU · " + E(env.workstation.memoryMB) + " MB · " + E(env.workstation.diskGB) + " GB</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">隔离根</span><span class="mono wrap">' + E(policy.root) + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">无网策略</span><span class="mono wrap">' + E(policy.noNetwork) + "</span></div>" +
          '<div class="list-row"><span class="tiny muted" style="width:110px">出网策略</span><span class="mono wrap">' + E(policy.network) + "</span></div>" +
        "</div>" +
      "</section>";

    function collect(attr, keyAttr) {
      return Array.from(root.querySelectorAll("[" + attr + "]")).map(function (row) {
        return row.querySelector("[" + keyAttr + "]").value;
      });
    }

    root.querySelector('[data-act="add-var"]').addEventListener("click", function () {
      const list = root.querySelector("#var-list");
      if (list.querySelector(".empty")) list.innerHTML = "";
      const row = document.createElement("div");
      row.className = "row";
      row.setAttribute("data-var", "new");
      row.style.marginBottom = "8px";
      row.innerHTML = '<input class="input mono" data-k style="width:220px" placeholder="KEY" />' +
        '<span class="muted mono">=</span><input class="input mono" data-v style="flex:1" placeholder="value" />' +
        '<button class="btn small ghost" data-del-var>' + A.icon("trash", 13) + "</button>";
      list.appendChild(row);
    });

    root.querySelector("#var-list").addEventListener("click", function (e) {
      if (e.target.closest("[data-del-var]")) e.target.closest(".row").remove();
    });

    root.querySelector('[data-act="save-vars"]').addEventListener("click", async function () {
      const vars = Array.from(root.querySelectorAll("#var-list .row")).map(function (row) {
        return {
          key: row.querySelector("[data-k]").value.trim(),
          value: row.querySelector("[data-v]").value,
        };
      }).filter(function (v) { return v.key; });
      try {
        await A.api("/api/env/vars", { method: "PUT", body: { vars: vars } });
        A.toast("环境变量已保存", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="add-mount"]').addEventListener("click", function () {
      const list = root.querySelector("#mount-list");
      if (list.querySelector(".empty")) list.innerHTML = "";
      const row = document.createElement("div");
      row.className = "row";
      row.setAttribute("data-mount", "new");
      row.style.marginBottom = "8px";
      row.innerHTML = '<input class="input mono" data-m style="flex:1" placeholder="/Users/you/Projects" />' +
        '<button class="btn small ghost" data-del-mount>' + A.icon("trash", 13) + "</button>";
      list.appendChild(row);
    });

    root.querySelector("#mount-list").addEventListener("click", function (e) {
      if (e.target.closest("[data-del-mount]")) e.target.closest(".row").remove();
    });

    root.querySelector('[data-act="save-mounts"]').addEventListener("click", async function () {
      const mounts = collect("data-mount", "data-m").map(function (v) { return v.trim(); }).filter(Boolean);
      try {
        await A.api("/api/env/mounts", { method: "PUT", body: { mounts: mounts } });
        A.toast("挂载目录已保存", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="save-locale"]').addEventListener("click", async function () {
      try {
        await A.api("/api/system/locale", {
          method: "PATCH",
          body: {
            followVps: !!locale.followVps,
            lang: root.querySelector("#locale-lang").value,
            timezone: root.querySelector("#locale-tz").value.trim(),
          },
        });
        A.toast("语言环境已应用，新开终端立即生效", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-toggle="locale-vps"]')?.addEventListener("click", async function () {
      const next = !locale.followVps;
      try {
        await A.api("/api/system/locale", { method: "PATCH", body: { followVps: next } });
        A.toast(next ? "已跟随 VPS 地区" : "已改为手动配置", "ok");
        A.render();
      } catch (err) {
        A.toast(err.message, "err");
      }
    });

    root.querySelector('[data-act="save-desktop"]').addEventListener("click", async function () {
      try {
        await A.api("/api/env/desktop", {
          method: "PUT",
          body: {
            host: root.querySelector("#vnc-host").value.trim(),
            port: Number(root.querySelector("#vnc-port").value),
            password: root.querySelector("#vnc-pass").value,
            scale: root.querySelector("#vnc-scale").value,
          },
        });
        A.toast("桌面配置已保存并重新检测", "ok");
        A.render();
      } catch (err) { A.toast(err.message, "err"); }
    });
  };
})();
