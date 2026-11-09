/* eslint-disable */
// 开通链接导入：解析 → 勾选安装清单 → 落配置 / 装公钥 / 建隧道 / 提交安装任务
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;

  const STATE = {
    link: "",
    payload: null,
    tools: [],
    selected: {},
    installKey: true,
    startTunnel: false,
    runSetup: false,
    preflight: {},
    warnings: [],
  };

  function reset() {
    STATE.link = "";
    STATE.payload = null;
    STATE.tools = [];
    STATE.selected = {};
    STATE.installKey = true;
    STATE.startTunnel = false;
    STATE.runSetup = false;
    STATE.preflight = {};
    STATE.warnings = [];
  }

  function toolRow(t) {
    const on = !!STATE.selected[t.id];
    return (
      '<div class="list-row" data-tool="' + E(t.id) + '">' +
        '<div style="flex:1;min-width:0">' +
          '<div style="display:flex;align-items:center;gap:8px">' +
            '<span style="font-weight:600">' + E(t.label) + "</span>" +
            '<span class="tiny muted">' + E(t.size) + "</span>" +
            (t.installed ? '<span class="chip green">已安装</span>' : "") +
            (t.needVm ? '<span class="chip amber">需虚拟机</span>' : "") +
          "</div>" +
          '<div class="tiny muted" style="margin-top:2px">' + E(t.desc) + "</div>" +
        "</div>" +
        '<button class="switch ' + (on ? "on" : "") + '" data-pick="' + E(t.id) + '" aria-label="选择"></button>' +
      "</div>"
    );
  }

  function optionRow(id, title, desc, on, disabled) {
    return (
      '<div class="list-row">' +
        '<div style="flex:1;min-width:0">' +
          '<div style="font-weight:600">' + E(title) + "</div>" +
          '<div class="tiny muted">' + E(desc) + "</div>" +
        "</div>" +
        '<button class="switch ' + (on ? "on" : "") + (disabled ? " disabled" : "") + '" data-opt="' + id + '"></button>' +
      "</div>"
    );
  }

  function bodyHTML() {
    const p = STATE.payload || {};
    const pf = STATE.preflight || {};
    const warn = (STATE.warnings || [])
      .map(function (w) { return '<div class="hint-box" style="margin-top:8px">' + E(w) + "</div>"; })
      .join("");

    return (
      '<div class="card-desc" style="margin:0 0 12px">' +
        E(p.name || "Vmbra 工作站") + " · 出口节点 " + E(p.host) + ":" + E(p.port) + "（" + E(p.user) + "）" +
      "</div>" +
      (p.note ? '<div class="hint-box" style="margin-bottom:10px">' + E(p.note) + "</div>" : "") +
      warn +
      '<div class="card-title" style="margin:16px 0 6px">需要下载的工具（可取消勾选）</div>' +
      '<div data-tools>' + STATE.tools.map(toolRow).join("") + "</div>" +
      '<div class="card-title" style="margin:16px 0 6px">导入时执行</div>' +
      optionRow("installKey", "自动写入本机公钥",
        p.hasPassword ? "用链接里的口令登录一次，把公钥追加到 VPS 的 authorized_keys；口令不会保存" : "链接不含口令，需要手动安装公钥",
        STATE.installKey, !p.hasPassword) +
      optionRow("startTunnel", "立即建立出口隧道", "导入后按该 VPS 建立 SOCKS5 出口与端口转发", STATE.startTunnel, false) +
      optionRow("runSetup", "立即开始安装", pf.licensed
        ? "按勾选项运行一键配置环境；会下载 Node / Git / Claude Code 等，耗时取决于网络"
        : "尚未激活授权，先导入配置，激活后再安装", STATE.runSetup, !pf.licensed) +
      '<div class="hint-box" style="margin-top:12px">导入只写入配置与密钥，不会在未勾选的情况下下载任何工具。</div>'
    );
  }

  function stepsHTML(steps) {
    const ICON = { ok: "✓", skip: "–", fail: "✗" };
    const COLOR = { ok: "green", skip: "gray", fail: "red" };
    return (
      '<div class="card-title" style="margin:0 0 8px">导入结果</div>' +
      steps.map(function (s) {
        return (
          '<div class="list-row">' +
            '<span class="chip ' + (COLOR[s.status] || "gray") + '">' + (ICON[s.status] || "·") + "</span>" +
            '<span style="flex:1;min-width:0">' + E(s.label) + "</span>" +
            '<span class="tiny muted" style="max-width:52%;text-align:right">' + E(s.detail || "") + "</span>" +
          "</div>"
        );
      }).join("")
    );
  }

  async function preview(link, m) {
    m.body.innerHTML = '<div class="loading-row"><span class="spinner"></span> 正在解析链接…</div>';
    try {
      const res = await A.api("/api/import/preview", { method: "POST", body: { link: link } });
      STATE.link = link;
      STATE.payload = res.payload || {};
      STATE.tools = res.tools || [];
      STATE.preflight = res.preflight || {};
      STATE.warnings = res.warnings || [];
      STATE.selected = {};
      STATE.tools.forEach(function (t) { if (t.selected) STATE.selected[t.id] = true; });
      STATE.installKey = !!STATE.payload.hasPassword;
      STATE.startTunnel = false;
      STATE.runSetup = false;
      m.body.innerHTML = bodyHTML();
      bind(m);
      m.foot.innerHTML =
        '<button class="btn" data-cancel>取消</button>' +
        '<button class="btn primary" data-apply>开始配置</button>';
      m.foot.querySelector("[data-cancel]").addEventListener("click", m.close);
      m.foot.querySelector("[data-apply]").addEventListener("click", function () { apply(m); });
    } catch (err) {
      m.body.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>";
      m.foot.innerHTML = '<button class="btn" data-cancel>关闭</button>';
      m.foot.querySelector("[data-cancel]").addEventListener("click", m.close);
    }
  }

  function bind(m) {
    m.body.querySelectorAll("[data-pick]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        const id = btn.dataset.pick;
        STATE.selected[id] = !STATE.selected[id];
        btn.classList.toggle("on", !!STATE.selected[id]);
      });
    });
    m.body.querySelectorAll("[data-opt]").forEach(function (btn) {
      if (btn.classList.contains("disabled")) return;
      btn.addEventListener("click", function () {
        const key = btn.dataset.opt;
        STATE[key] = !STATE[key];
        btn.classList.toggle("on", !!STATE[key]);
      });
    });
  }

  async function apply(m) {
    const tools = Object.keys(STATE.selected).filter(function (k) { return STATE.selected[k]; });
    if (!tools.length && !STATE.startTunnel) {
      A.toast("至少勾选一个工具，或选择立即建立出口隧道", "err");
      return;
    }
    const btn = m.foot.querySelector("[data-apply]");
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 配置中';

    try {
      const res = await A.api("/api/import/apply", {
        method: "POST",
        body: {
          link: STATE.link,
          tools: tools,
          installKey: STATE.installKey,
          startTunnel: STATE.startTunnel,
          runSetup: STATE.runSetup,
        },
      });
      m.body.innerHTML = stepsHTML(res.steps || []);
      m.foot.innerHTML =
        (res.taskId ? '<button class="btn" data-tasks>查看安装进度</button>' : "") +
        '<button class="btn primary" data-done>完成</button>';
      m.foot.querySelector("[data-done]").addEventListener("click", function () {
        m.close();
        A.render();
      });
      const btnTasks = m.foot.querySelector("[data-tasks]");
      if (btnTasks) {
        btnTasks.addEventListener("click", function () {
          m.close();
          location.hash = "#/tasks";
        });
      }
      A.toast("配置已导入", "ok");
    } catch (err) {
      const detail = err.detail && err.detail.steps ? stepsHTML(err.detail.steps) : "";
      m.body.innerHTML = '<div class="hint-box">' + E(err.message) + "</div>" + detail;
      btn.disabled = false;
      btn.innerHTML = "重试";
      btn.addEventListener("click", function () { apply(m); }, { once: true });
    }
  }

  // 入口：link 为空时显示粘贴框
  A.openImport = function (link) {
    reset();
    const m = A.modal({
      title: "导入开通配置",
      body:
        '<div class="card-desc" style="margin:0 0 10px">粘贴服务商发来的开通链接（vmbra://import/…），或直接拖入 .vmbrapkg 文件内容。</div>' +
        '<textarea class="input" data-link rows="5" spellcheck="false" placeholder="vmbra://import/… 或 http://127.0.0.1:7788/#/settings?import=…"></textarea>',
      footer:
        '<button class="btn" data-cancel>取消</button>' +
        '<button class="btn primary" data-parse>解析</button>',
    });
    m.foot.querySelector("[data-cancel]").addEventListener("click", m.close);
    const input = m.body.querySelector("[data-link]");

    if (link) {
      input.value = link;
      preview(link, m);
      return;
    }
    m.foot.querySelector("[data-parse]").addEventListener("click", function () {
      const v = (input.value || "").trim();
      if (!v) {
        A.toast("请粘贴开通链接", "err");
        return;
      }
      preview(v, m);
    });
    input.focus();
  };

  // 从 URL 里取一次性导入参数（控制台地址或桌面深链都会带 import=）
  A.takeImportParam = function () {
    const search = new URLSearchParams(location.search);
    const hash = location.hash || "";
    let value = search.get("import") || "";
    const idx = hash.indexOf("import=");
    if (!value && idx >= 0) {
      value = hash.slice(idx + "import=".length).split("&")[0];
    }
    if (!value) return "";
    const url = new URL(location.href);
    url.searchParams.delete("import");
    let nextHash = hash.replace(/([?&])import=[^&]*/, "").replace(/[?&]$/, "");
    if (nextHash === "#/" ) nextHash = "#/settings";
    history.replaceState(null, "", url.pathname + (url.search || "") + nextHash);
    return value;
  };
})();
