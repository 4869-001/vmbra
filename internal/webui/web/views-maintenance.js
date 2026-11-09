/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  A.views.maintenance = async function (root) {
    const data = await A.api("/api/maintenance");
    const snaps = data.snapshots || [];
    const storage = data.storage || {};
    const ws = data.workstation || {};

    root.innerHTML =
      '<section class="card">' +
        '<div class="card-head">' +
          '<div><div class="card-title">快照</div>' +
            '<div class="card-sub">保存工作区当前状态，可随时回滚</div></div>' +
          '<button class="btn primary" data-act="snap">' + A.icon("plus", 14) + "创建快照</button>" +
        "</div>" +
        '<div class="card-body">' +
          (snaps.length
            ? '<table class="table"><thead><tr><th>名称</th><th>创建时间</th><th>大小</th><th></th></tr></thead><tbody>' +
              snaps.map(function (s) {
                return "<tr>" +
                  "<td>" + E(s.name) + '<div class="tiny muted mono">' + E(s.id) + "</div></td>" +
                  '<td class="muted">' + A.fmtTime(s.createdAt) + "</td>" +
                  '<td class="muted">' + A.fmtBytes(s.sizeBytes) + "</td>" +
                  '<td class="actions">' +
                    '<button class="btn small" data-restore="' + E(s.id) + '">恢复</button> ' +
                    '<button class="btn small ghost" data-del="' + E(s.id) + '">' + A.icon("trash", 13) + "</button>" +
                  "</td>" +
                "</tr>";
              }).join("") +
              "</tbody></table>"
            : '<div class="empty">还没有快照</div>') +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">存储占用</div>' +
          '<div class="card-sub">数据目录 ' + E(storage.dataDir || "") + "</div></div></div>" +
        '<div class="card-body stats">' +
          '<div class="stat"><div class="k">工作区</div><div class="v">' + A.fmtBytes(storage.workspaceBytes) + "</div></div>" +
          '<div class="stat"><div class="k">快照</div><div class="v">' + A.fmtBytes(storage.snapshotBytes) + "</div></div>" +
          '<div class="stat"><div class="k">数据目录总计</div><div class="v">' + A.fmtBytes(storage.dataDirBytes) + "</div></div>" +
          '<div class="stat"><div class="k">磁盘可用</div><div class="v">' + A.fmtBytes(storage.freeBytes) + "</div></div>" +
        "</div>" +
        '<div class="card-body">' +
          '<div class="bar"><i style="width:' +
            Math.min(100, storage.totalBytes ? (storage.dataDirBytes / storage.totalBytes) * 100 : 0).toFixed(2) +
            '%"></i></div>' +
          '<div class="tiny muted" style="margin-top:6px">数据目录占磁盘 ' +
            (storage.totalBytes ? ((storage.dataDirBytes / storage.totalBytes) * 100).toFixed(3) : "0") + "%</div>" +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-head"><div><div class="card-title">维护与恢复</div>' +
          '<div class="card-sub">重置会清空工作区内容，快照保持不变</div></div>' +
          '<div class="row">' +
            '<button class="btn" data-act="export">' + A.icon("download", 13) + "导出工作区</button>" +
            '<button class="btn" data-act="finder">' + A.icon("external", 13) + "在 Finder 中打开</button>" +
            '<button class="btn danger" data-act="reset">重置工作站</button>' +
          "</div></div>" +
        (ws.state === "running"
          ? '<div class="card-body"><div class="hint-box">工作站正在运行：恢复快照与重置需要先停止工作站。</div></div>'
          : "") +
      "</section>";

    root.querySelector('[data-act="snap"]').addEventListener("click", function () {
      const m = A.modal({
        title: "创建快照",
        narrow: true,
        body: '<div class="field"><label>快照名称</label><input class="input" id="snap-name" placeholder="例如 安装完依赖" value="快照 ' +
          new Date().toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }) + '" /></div>',
        footer: '<button class="btn" data-cancel>取消</button><button class="btn primary" data-go>创建</button>',
      });
      m.foot.querySelector("[data-cancel]").addEventListener("click", m.close);
      m.foot.querySelector("[data-go]").addEventListener("click", async function () {
        const name = m.body.querySelector("#snap-name").value;
        try {
          const res = await A.api("/api/maintenance/snapshots", { method: "POST", body: { name: name } });
          m.close();
          openLog(res.taskId, "创建快照");
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    function openLog(taskId, title) {
      const m = A.modal({
        title: title,
        body: '<div class="log" id="lg"></div>',
        footer: '<span class="tiny muted" style="margin-right:auto" id="lg-state">执行中…</span><button class="btn" data-c>关闭</button>',
      });
      const log = m.body.querySelector("#lg");
      const stateEl = m.foot.querySelector("#lg-state");
      const wsx = A.trackTask(taskId, function (chunk) {
        log.textContent += chunk;
        log.scrollTop = log.scrollHeight;
      }, function (task) {
        if (!task) { stateEl.textContent = "状态未知"; return; }
        stateEl.textContent = task.status === "succeeded" ? "已完成" : task.status === "failed" ? "失败：" + (task.error || "") : task.status;
        if (task.status === "succeeded") setTimeout(function () { if (document.body.contains(m.wrap)) { m.close(); A.render(); } }, 900);
      });
      m.foot.querySelector("[data-c]").addEventListener("click", function () { try { wsx.close(); } catch (e) {} m.close(); });
    }

    root.querySelectorAll("[data-restore]").forEach(function (el) {
      el.addEventListener("click", async function () {
        const okGo = await A.confirm("恢复快照", "将用该快照覆盖当前工作区内容，当前未保存的修改会丢失。", true);
        if (!okGo) return;
        try {
          const res = await A.api("/api/maintenance/snapshots/" + el.dataset.restore + "/restore", { method: "POST" });
          openLog(res.taskId, "恢复快照");
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    root.querySelectorAll("[data-del]").forEach(function (el) {
      el.addEventListener("click", async function () {
        const okGo = await A.confirm("删除快照", "删除后无法恢复该快照。", true);
        if (!okGo) return;
        try {
          await A.api("/api/maintenance/snapshots/" + el.dataset.del, { method: "DELETE" });
          A.toast("快照已删除", "ok");
          A.render();
        } catch (err) { A.toast(err.message, "err"); }
      });
    });

    root.querySelector('[data-act="export"]').addEventListener("click", async function () {
      const btn = this;
      const label = btn.innerHTML;
      btn.disabled = true;
      btn.textContent = "打包中…";
      try {
        const res = await A.api("/api/maintenance/export");
        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
        a.href = url;
        a.download = "anywhere-workspace-" + stamp + ".tar.gz";
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(function () { URL.revokeObjectURL(url); }, 5000);
        A.toast("工作区已导出（" + A.fmtBytes(blob.size) + "）", "ok");
      } catch (err) {
        A.toast(err.message, "err");
      } finally {
        btn.disabled = false;
        btn.innerHTML = label;
      }
    });

    root.querySelector('[data-act="finder"]').addEventListener("click", async function () {
      try {
        await A.api("/api/maintenance/open-finder", { method: "POST" });
      } catch (err) { A.toast(err.message, "err"); }
    });

    root.querySelector('[data-act="reset"]').addEventListener("click", async function () {
      const okGo = await A.confirm("重置工作站", "将清空工作区内的全部文件（快照保留），且需要处于停止状态。", true);
      if (!okGo) return;
      try {
        const res = await A.api("/api/maintenance/reset", { method: "POST" });
        openLog(res.taskId, "重置工作站");
      } catch (err) { A.toast(err.message, "err"); }
    });
  };
})();
