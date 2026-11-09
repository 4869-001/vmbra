/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  const ICON = { ok: "check", warn: "alert", fail: "alert" };
  const LABEL = { ok: "正常", warn: "注意", fail: "异常" };

  A.views.diagnostics = async function (root) {
    const data = await A.api("/api/diagnostics");
    const checks = data.checks || [];
    const summary = data.summary || { ok: 0, warn: 0, fail: 0 };

    root.innerHTML =
      '<section class="card card-tight">' +
        '<div class="card-head">' +
          '<div><div class="card-title">系统诊断</div>' +
            '<div class="card-sub">生成于 ' + A.fmtTime(data.generatedAt) + " · " + E(data.runtime || "") + " · v" + E(data.version || "") + "</div></div>" +
          '<div class="row">' +
            '<button class="btn small" data-act="rerun">' + A.icon("refresh", 13) + "重新检测</button>" +
            '<a class="btn small" href="/api/diagnostics/bundle?token=' + encodeURIComponent(A.token) + '">' + A.icon("download", 13) + "导出诊断包</a>" +
          "</div>" +
        "</div>" +
        '<div class="card-body row">' +
          '<span class="chip green">正常 ' + summary.ok + "</span>" +
          '<span class="chip amber">注意 ' + summary.warn + "</span>" +
          '<span class="chip red">异常 ' + summary.fail + "</span>" +
        "</div>" +
      "</section>" +

      '<section class="card">' +
        '<div class="card-body" style="margin:0">' +
          checks.map(function (c) {
            return '<div class="check">' +
              '<span class="check-icon ' + c.status + '">' + A.icon(ICON[c.status] || "dot", 18) + "</span>" +
              "<div style=\"flex:1\">" +
                '<div class="check-title">' + E(c.title) + " " +
                  '<span class="chip ' + (c.status === "ok" ? "green" : c.status === "warn" ? "amber" : "red") + '" style="margin-left:6px">' +
                  (LABEL[c.status] || c.status) + "</span></div>" +
                '<div class="check-detail">' + E(c.detail) + "</div>" +
                (c.hint ? '<div class="check-hint">' + E(c.hint) + "</div>" : "") +
              "</div>" +
            "</div>";
          }).join("") +
        "</div>" +
      "</section>";

    root.querySelector('[data-act="rerun"]').addEventListener("click", function () { A.render(); });
  };
})();
