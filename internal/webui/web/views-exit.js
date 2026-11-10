/* eslint-disable */
/**
 * 出口视图：用户只做两件事——粘贴一条订阅链接、点「一键对齐」。
 * 语言、文字、时区、浏览器出口全部由后端根据节点所在地区自动写入；
 * 闸门状态、检查证据、节点测速都在同一页给出。
 */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  function chip(cls, text) {
    return '<span class="chip ' + cls + '">' + E(text) + "</span>";
  }

  function stateChip(gate) {
    if (!gate) return chip("gray", "未检查");
    const st = gate.state || "closed";
    if (st === "open") return chip("green", "已放行 · 剩 " + Math.max(0, Math.round((gate.remainingMs || 0) / 1000)) + "s");
    if (st === "checking") return chip("amber", "检查中");
    return chip("red", "已切断");
  }

  function checksTable(gate) {
    const checks = (gate && gate.report && gate.report.checks) || [];
    if (!checks.length) return '<div class="empty">还没有检查记录</div>';
    return '<table class="table"><thead><tr><th style="width:120px">检查项</th><th style="width:70px">结果</th><th>证据</th></tr></thead><tbody>' +
      checks.map(function (c) {
        return "<tr><td>" + E(c.name) + "</td><td>" +
          (c.ok ? '<span class="chip green">通过</span>' : '<span class="chip red">异常</span>') +
          '</td><td class="tiny muted mono">' + E(c.detail || "") + "</td></tr>";
      }).join("") + "</tbody></table>" +
      (gate.report && gate.report.at ? '<div class="tiny muted" style="margin-top:8px">最近检查：' + E(gate.report.at.replace("T", " ").slice(0, 19)) + "</div>" : "");
  }

  A.views.exit = async function (root) {
    const [view, system, gate] = await Promise.all([
      A.api("/api/proxy"),
      A.api("/api/system"),
      A.api("/api/egress"),
    ]);
    const core = view.core || {};
    const sources = view.sources || [];
    const locale = (system && system.locale) || {};
    const nodes = [];
    sources.forEach(function (src) {
      (src.nodes || []).forEach(function (n) {
        nodes.push(n);
      });
    });
    const coreChip = core.running ? chip("green", "内核运行中") : view.available ? chip("gray", "内核就绪") : chip("red", "未找到 mihomo");

    const summary = core.running
      ? '<div class="grid-3">' +
        '<div class="field"><label>节点</label><div class="mono">' + E(core.nodeName || "-") + "</div></div>" +
        '<div class="field"><label>出口 IP</label><div class="mono">' + E(core.exitIp || "-") + "</div></div>" +
        '<div class="field"><label>地区</label><div>' + E([core.country, core.city].filter(Boolean).join(" · ") || "-") + "</div></div>" +
        '<div class="field"><label>系统语言</label><div class="mono">' + E(locale.lang || "-") + "</div></div>" +
        '<div class="field"><label>时区</label><div class="mono">' + E(locale.timezone || "-") + "</div></div>" +
        '<div class="field"><label>入口一致</label><div>' + stateChip(gate) + "</div></div>" +
        "</div>"
      : '<div class="empty">还没有配置出口。粘贴订阅链接后点「导入」，再点「一键对齐」即可。</div>';

    const nodeOptions = nodes
      .map(function (n) {
        const sel = n.id === view.selected ? " selected" : "";
        return '<option value="' + E(n.id) + '"' + sel + ">" + E(n.name) + " · " + E(n.type || "") + "</option>";
      })
      .join("");

    const sourceRows = sources.length
      ? '<table class="table"><thead><tr><th>订阅</th><th>节点</th><th>自动刷新</th><th>更新时间</th><th style="width:110px"></th></tr></thead><tbody>' +
        sources.map(function (s) {
          return "<tr><td>" + E(s.name) + '</td><td class="mono">' + (s.nodes || []).length + "</td><td>" +
            (s.intervalHours ? E(s.intervalHours + " 小时") : '<span class="muted">关闭</span>') +
            '</td><td class="tiny muted">' + E((s.updatedAt || "").slice(0, 16).replace("T", " ")) + "</td>" +
            '<td class="actions">' +
            (s.url ? '<button class="btn small ghost" data-refresh="' + E(s.id) + '">' + A.icon("refresh", 13) + "</button>" : "") +
            '<button class="btn small ghost" data-del-source="' + E(s.id) + '">' + A.icon("trash", 13) + "</button></td></tr>";
        }).join("") + "</tbody></table>"
      : '<div class="empty">暂无订阅</div>';

    root.innerHTML =
      '<section class="card card-tight">' +
      '<div class="card-head"><div style="max-width:640px">' +
      '<div class="card-title">出口与地区</div>' +
      '<div class="card-desc">导入 Clash 订阅后选一个节点，Vmbra 会自动把系统语言、文字、时区与浏览器出口对齐到该节点所在地区。</div>' +
      "</div>" + coreChip + "</div>" +
      '<div class="card-body">' +
      '<div class="grid-2">' +
      '<div class="field"><label>订阅链接</label><input class="input mono" id="exit-link" placeholder="https://example.com/api/v1/client/subscribe?token=..." /></div>' +
      '<div class="field"><label>&nbsp;</label><div class="form-actions"><button class="btn primary" data-act="exit-import">' + A.icon("download", 13) + "导入订阅</button></div></div>" +
      "</div>" +
      '<details class="tiny muted" style="margin-top:4px"><summary>没有链接？直接粘贴配置</summary>' +
      '<div style="margin-top:8px"><textarea class="input mono" id="exit-content" rows="6" placeholder="proxies:&#10;  - {name: LA, type: ss, server: 1.2.3.4, port: 8443, cipher: aes-256-gcm, password: secret}"></textarea>' +
      '<div class="form-actions"><button class="btn" data-act="exit-import-text">解析粘贴内容</button></div></div></details>' +
      "</div></section>" +

      '<section class="card">' +
      '<div class="card-head"><div><div class="card-title">一键对齐</div>' +
      '<div class="card-sub">选节点 → 启动内核 → 探测出口 → 写入语言/时区/代理</div></div>' + stateChip(gate) + "</div>" +
      '<div class="card-body">' +
      (nodes.length
        ? '<div class="grid-3">' +
          '<div class="field"><label>节点</label><select class="input" id="exit-node">' + nodeOptions + "</select></div>" +
          '<div class="field"><label>&nbsp;</label><div class="form-actions">' +
          '<button class="btn primary" data-act="exit-apply">' + A.icon("play", 13) + "一键对齐</button>" +
          '<button class="btn" data-act="exit-delay">测速</button>' +
          (core.running ? '<button class="btn danger" data-act="exit-stop">停止出口</button>' : "") +
          "</div></div></div>" +
          '<div id="exit-delay-box"></div>' +
          '<div class="card-divider" style="margin-top:12px">' + summary + "</div>" +
          '<div class="form-actions" style="margin-top:10px">' +
          '<button class="btn" data-act="exit-check">立即检查</button>' +
          '<button class="btn" data-act="exit-repair">一键修复环境</button>' +
          "</div>"
        : '<div class="empty">先导入订阅，这里会出现节点列表。</div>') +
      "</div></section>" +

      '<section class="card">' +
      '<div class="card-head"><div><div class="card-title">闸门与证据</div>' +
      '<div class="card-sub">检查通过才放行，租约到期或检查失败立即切断</div></div>' + stateChip(gate) + "</div>" +
      '<div class="card-body">' + checksTable(gate) +
      (gate && gate.reason ? '<div class="hint-box" style="margin-top:8px">' + E(gate.reason) + "</div>" : "") +
      "</div></section>" +

      '<section class="card">' +
      '<div class="card-head"><div><div class="card-title">订阅来源</div>' +
      '<div class="card-sub">同名订阅再次导入会覆盖刷新，默认每 6 小时自动刷新</div></div></div>' +
      '<div class="card-body">' + sourceRows +
      (view.coreBinary ? '<div class="tiny muted" style="margin-top:10px">内核：<span class="mono">' + E(view.coreBinary) + "</span></div>" : "") +
      "</div></section>";

    const $ = function (sel) {
      return root.querySelector(sel);
    };
    const refreshView = function () {
      return A.views.exit(root);
    };

    async function post(path, body) {
      return A.api(path, { method: "POST", body: JSON.stringify(body || {}) });
    }

    if ($('[data-act="exit-import"]')) {
      $('[data-act="exit-import"]').onclick = async function () {
        const url = ($("#exit-link").value || "").trim();
        if (!url) return A.toast("请先填写订阅链接", "err");
        try {
          await post("/api/proxy/import", { url: url });
          A.toast("订阅已导入", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    }
    if ($('[data-act="exit-import-text"]')) {
      $('[data-act="exit-import-text"]').onclick = async function () {
        const content = ($("#exit-content").value || "").trim();
        if (!content) return A.toast("请先粘贴配置内容", "err");
        try {
          await post("/api/proxy/import", { content: content });
          A.toast("订阅已导入", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    }
    if ($('[data-act="exit-apply"]')) {
      $('[data-act="exit-apply"]').onclick = async function () {
        const nodeId = ($("#exit-node") || {}).value;
        if (!nodeId) return A.toast("请选择一个节点", "err");
        const btn = this;
        btn.disabled = true;
        btn.textContent = "正在对齐…";
        try {
          const res = await post("/api/proxy/apply", { nodeId: nodeId });
          A.toast("已对齐到 " + (res.country || res.exitIp || "节点") + " · " + (res.lang || ""), "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
          btn.disabled = false;
          btn.textContent = "一键对齐";
        }
      };
    }
    if ($('[data-act="exit-delay"]')) {
      $('[data-act="exit-delay"]').onclick = async function () {
        const box = $("#exit-delay-box");
        box.innerHTML = '<div class="tiny muted" style="margin-top:8px">测速中…</div>';
        try {
          const res = await post("/api/proxy/delay", {});
          const rows = (res.results || []).map(function (r) {
            const node = nodes.filter(function (n) { return n.name === r.name; })[0] || {};
            return "<tr><td>" + E(r.name) + "</td><td>" + (r.error ? '<span class="chip red">超时</span>' : '<span class="chip green">' + r.delayMs + " ms</span>") +
              '</td><td class="actions">' +
              (r.error ? "" : '<button class="btn small" data-pick="' + E(node.id || "") + '">切换</button>') +
              "</td></tr>";
          }).join("");
          box.innerHTML = '<div class="card-divider" style="margin-top:10px"><table class="table"><thead><tr><th>节点</th><th style="width:90px">延迟</th><th style="width:80px"></th></tr></thead><tbody>' + rows + "</tbody></table></div>";
          box.querySelectorAll("[data-pick]").forEach(function (b) {
            b.onclick = async function () {
              const id = b.getAttribute("data-pick");
              if (!id) return;
              try {
                await post("/api/proxy/select", { nodeId: id });
                A.toast("已切换节点", "ok");
                refreshView();
              } catch (err) {
                A.toast(err.message, "err");
              }
            };
          });
        } catch (err) {
          box.innerHTML = '<div class="hint-box" style="margin-top:8px">' + E(err.message) + "</div>";
        }
      };
    }
    if ($('[data-act="exit-stop"]')) {
      $('[data-act="exit-stop"]').onclick = async function () {
        try {
          await post("/api/proxy/stop", {});
          A.toast("出口已停止", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    }
    if ($('[data-act="exit-check"]')) {
      $('[data-act="exit-check"]').onclick = async function () {
        try {
          const snap = await post("/api/egress/check", {});
          A.toast(snap.allowed ? "检查通过，已放行" : "检查未通过：" + (snap.reason || ""), snap.allowed ? "ok" : "err");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    }
    if ($('[data-act="exit-repair"]')) {
      $('[data-act="exit-repair"]').onclick = async function () {
        try {
          await post("/api/egress/repair", {});
          A.toast("环境已按当前出口重新对齐", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    }
    root.querySelectorAll("[data-refresh]").forEach(function (b) {
      b.onclick = async function () {
        try {
          await post("/api/proxy/refresh", { sourceId: b.getAttribute("data-refresh") });
          A.toast("订阅已刷新", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    });
    root.querySelectorAll("[data-del-source]").forEach(function (b) {
      b.onclick = async function () {
        try {
          await A.api("/api/proxy/sources/" + encodeURIComponent(b.getAttribute("data-del-source")), { method: "DELETE" });
          A.toast("订阅已删除", "ok");
          refreshView();
        } catch (err) {
          A.toast(err.message, "err");
        }
      };
    });
  };
})();
