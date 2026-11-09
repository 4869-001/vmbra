/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  const STATUS = {
    queued: ["排队中", "amber"],
    running: ["执行中", "blue"],
    succeeded: ["成功", "green"],
    failed: ["失败", "red"],
    canceled: ["已取消", "gray"],
  };

  function chip(status) {
    const s = STATUS[status] || [status, "gray"];
    return '<span class="chip ' + s[1] + '">' + s[0] + "</span>";
  }

  function duration(t) {
    if (!t.startedAt) return "—";
    const end = t.finishedAt ? new Date(t.finishedAt) : new Date();
    const ms = end - new Date(t.startedAt);
    if (ms < 1000) return ms + " ms";
    if (ms < 60000) return (ms / 1000).toFixed(1) + " s";
    return Math.round(ms / 60000) + " min";
  }

  A.views.tasks = async function (root) {
    const data = await A.api("/api/tasks");
    const tasks = data.tasks || [];
    const summary = data.summary || {};

    root.innerHTML =
      '<section class="card">' +
        '<div class="card-head">' +
          '<div><div class="card-title">任务队列</div>' +
            '<div class="card-sub">共 ' + (summary.total || 0) + " 个任务 · 执行中 " + (summary.running || 0) +
              " · 排队 " + (summary.queued || 0) + " · 失败 " + (summary.failed || 0) + "</div></div>" +
          '<button class="btn small" data-act="reload">' + A.icon("refresh", 13) + "刷新</button>" +
        "</div>" +
        '<div class="card-body">' +
          (tasks.length
            ? '<table class="table"><thead><tr><th>任务</th><th>类型</th><th>状态</th><th>耗时</th><th>创建时间</th></tr></thead><tbody>' +
              tasks.map(function (t) {
                return '<tr data-task="' + E(t.id) + '" style="cursor:pointer">' +
                  "<td>" + E(t.title) + "</td>" +
                  '<td class="muted">' + E(t.kind) + "</td>" +
                  "<td>" + chip(t.status) + "</td>" +
                  '<td class="muted">' + duration(t) + "</td>" +
                  '<td class="muted">' + A.fmtTime(t.createdAt) + "</td>" +
                "</tr>";
              }).join("") +
              "</tbody></table>"
            : '<div class="empty">还没有任务</div>') +
        "</div>" +
      "</section>";

    root.querySelector('[data-act="reload"]').addEventListener("click", function () { A.render(); });

    root.querySelectorAll("[data-task]").forEach(function (tr) {
      tr.addEventListener("click", function () { openTask(tr.dataset.task); });
    });

    async function openTask(id) {
      const m = A.modal({
        title: "任务详情",
        body: '<div class="row" style="margin-bottom:10px" id="meta"></div><div class="log" id="lg"></div>',
        footer: '<span class="tiny muted" style="margin-right:auto" id="st">…</span>' +
          '<button class="btn" data-cancel-task style="display:none">取消任务</button>' +
          '<button class="btn" data-c>关闭</button>',
      });
      const log = m.body.querySelector("#lg");
      const meta = m.body.querySelector("#meta");
      const st = m.foot.querySelector("#st");
      const cancelBtn = m.foot.querySelector("[data-cancel-task]");
      let socket = null;

      try {
        const detail = await A.api("/api/tasks/" + encodeURIComponent(id));
        meta.innerHTML = chip(detail.task.status) + '<span class="tiny muted">' + E(detail.task.kind) + " · " + E(detail.task.title) + "</span>";
        log.textContent = detail.output || "";
        st.textContent = detail.task.error || "";
        if (detail.task.status === "running" || detail.task.status === "queued") {
          cancelBtn.style.display = "";
          socket = A.trackTask(id, function (chunk) {
            log.textContent += chunk;
            log.scrollTop = log.scrollHeight;
          }, function (task) {
            if (!task) return;
            meta.innerHTML = chip(task.status) + '<span class="tiny muted">' + E(task.kind) + " · " + E(task.title) + "</span>";
            st.textContent = task.error || "";
            cancelBtn.style.display = "none";
          });
        }
      } catch (err) {
        log.textContent = err.message;
      }

      cancelBtn.addEventListener("click", async function () {
        try {
          await A.api("/api/tasks/" + encodeURIComponent(id) + "/cancel", { method: "POST" });
          A.toast("已请求取消", "ok");
        } catch (err) { A.toast(err.message, "err"); }
      });
      m.foot.querySelector("[data-c]").addEventListener("click", function () {
        if (socket) try { socket.close(); } catch (e) {}
        m.close();
      });
    }
  };
})();
