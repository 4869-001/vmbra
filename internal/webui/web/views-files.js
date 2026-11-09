/* eslint-disable */
(function () {
  "use strict";
  const A = window.App;
  const E = A.esc;
  let cwd = "";

  function crumbs(path) {
    const parts = path ? path.split("/") : [];
    let acc = "";
    let html = '<a data-path="">工作区</a>';
    parts.forEach(function (p) {
      acc = acc ? acc + "/" + p : p;
      html += '<span class="sep">/</span><a data-path="' + E(acc) + '">' + E(p) + "</a>";
    });
    return html;
  }

  function render(root) {
    root.innerHTML =
      '<section class="card">' +
        '<div class="card-head">' +
          '<div class="crumbs" id="crumbs">' + crumbs(cwd) + "</div>" +
          '<div class="row">' +
            '<button class="btn small" data-act="mkdir">' + A.icon("plus", 13) + "新建文件夹</button>" +
            '<button class="btn small" data-act="upload">' + A.icon("upload", 13) + "上传</button>" +
            '<button class="btn small" data-act="reload">' + A.icon("refresh", 13) + "刷新</button>" +
          "</div>" +
        "</div>" +
        '<div class="card-body" id="file-body">' +
          '<div class="loading-row"><span class="spinner"></span> 读取目录…</div>' +
        "</div>" +
        '<div class="dropzone" style="margin-top:12px" id="drop">将文件拖拽到这里上传到当前目录</div>' +
        '<input type="file" id="file-input" multiple style="display:none" />' +
      "</section>";

    const body = root.querySelector("#file-body");
    const input = root.querySelector("#file-input");

    async function load() {
      try {
        const data = await A.api("/api/files?path=" + encodeURIComponent(cwd));
        root.querySelector("#crumbs").innerHTML = crumbs(cwd);
        bindCrumbs();
        if (!data.entries.length) {
          body.innerHTML = '<div class="empty">该目录为空</div>';
          return;
        }
        body.innerHTML =
          '<table class="table"><thead><tr><th>名称</th><th style="width:100px">大小</th><th style="width:150px">修改时间</th><th style="width:110px"></th></tr></thead><tbody>' +
          data.entries.map(function (f) {
            return "<tr>" +
              '<td><div class="file-name" data-open="' + E(f.path) + '" data-dir="' + (f.dir ? "1" : "") + '">' +
                A.icon(f.dir ? "folder" : "file", 16) + "<span>" + E(f.name) + "</span></div></td>" +
              "<td class=\"muted\">" + (f.dir ? "—" : A.fmtBytes(f.size)) + "</td>" +
              '<td class="muted">' + A.fmtTime(f.modTime) + "</td>" +
              '<td class="actions">' +
                (f.dir ? "" : '<button class="btn small ghost" data-dl="' + E(f.path) + '" title="下载">' + A.icon("download", 13) + "</button>") +
                '<button class="btn small ghost" data-del="' + E(f.path) + '" title="删除">' + A.icon("trash", 13) + "</button>" +
              "</td>" +
            "</tr>";
          }).join("") +
          "</tbody></table>";
      } catch (err) {
        body.innerHTML = '<div class="empty">' + E(err.message) + "</div>";
      }
    }

    function bindCrumbs() {
      root.querySelectorAll("#crumbs a").forEach(function (a) {
        a.addEventListener("click", function () {
          cwd = a.dataset.path;
          load();
        });
        a.classList.toggle("current", a.dataset.path === cwd);
      });
    }

    async function uploadFiles(files) {
      if (!files || !files.length) return;
      for (const f of files) {
        const fd = new FormData();
        fd.append("file", f);
        try {
          await A.api("/api/files/upload?path=" + encodeURIComponent(cwd), { method: "POST", raw: fd });
          A.toast("已上传 " + f.name, "ok");
        } catch (err) {
          A.toast(f.name + " 上传失败：" + err.message, "err");
        }
      }
      load();
    }

    root.querySelector('[data-act="upload"]').addEventListener("click", function () { input.click(); });
    input.addEventListener("change", function () { uploadFiles(input.files); input.value = ""; });
    root.querySelector('[data-act="reload"]').addEventListener("click", load);
    root.querySelector('[data-act="mkdir"]').addEventListener("click", async function () {
      const name = prompt("新文件夹名称");
      if (!name) return;
      const target = cwd ? cwd + "/" + name : name;
      try {
        await A.api("/api/files/mkdir", { method: "POST", body: { path: target } });
        load();
      } catch (err) { A.toast(err.message, "err"); }
    });

    const drop = root.querySelector("#drop");
    ["dragenter", "dragover"].forEach(function (ev) {
      drop.addEventListener(ev, function (e) { e.preventDefault(); drop.classList.add("hot"); });
    });
    ["dragleave", "drop"].forEach(function (ev) {
      drop.addEventListener(ev, function (e) { e.preventDefault(); drop.classList.remove("hot"); });
    });
    drop.addEventListener("drop", function (e) {
      if (e.dataTransfer && e.dataTransfer.files) uploadFiles(e.dataTransfer.files);
    });

    body.addEventListener("click", async function (e) {
      const openEl = e.target.closest("[data-open]");
      if (openEl) {
        const p = openEl.dataset.open;
        if (openEl.dataset.dir) {
          cwd = p;
          load();
        } else {
          openEditor(p);
        }
        return;
      }
      const dl = e.target.closest("[data-dl]");
      if (dl) {
        location.href = "/api/files/download?token=" + encodeURIComponent(A.token) +
          "&path=" + encodeURIComponent(dl.dataset.dl);
        return;
      }
      const del = e.target.closest("[data-del]");
      if (del) {
        const okDel = await A.confirm("删除确认", "确定删除 " + del.dataset.del + " ？该操作不可撤销。", true);
        if (!okDel) return;
        try {
          await A.api("/api/files?path=" + encodeURIComponent(del.dataset.del), { method: "DELETE" });
          A.toast("已删除", "ok");
          load();
        } catch (err) { A.toast(err.message, "err"); }
      }
    });

    async function openEditor(path) {
      let data;
      try {
        data = await A.api("/api/files/content?path=" + encodeURIComponent(path));
      } catch (err) {
        A.toast(err.message, "err");
        return;
      }
      const binary = data.binary;
      const m = A.modal({
        title: path,
        body:
          (binary ? '<div class="hint-box" style="margin-bottom:10px">该文件不是文本格式，无法在线编辑，请下载后处理。</div>' : "") +
          '<textarea class="input" id="editor" style="width:100%;height:420px" ' + (binary ? "disabled" : "") + ">" + E(data.content) + "</textarea>",
        footer:
          '<span class="tiny muted" style="margin-right:auto">' + A.fmtBytes(data.size) + "</span>" +
          '<button class="btn" data-dl2>下载</button>' +
          '<button class="btn primary" data-save ' + (binary ? "disabled" : "") + ">保存</button>",
      });
      m.foot.querySelector("[data-dl2]").addEventListener("click", function () {
        location.href = "/api/files/download?token=" + encodeURIComponent(A.token) + "&path=" + encodeURIComponent(path);
      });
      m.foot.querySelector("[data-save]").addEventListener("click", async function () {
        const content = m.body.querySelector("#editor").value;
        try {
          await A.api("/api/files/content", { method: "PUT", body: { path: path, content: content } });
          A.toast("已保存", "ok");
          m.close();
          load();
        } catch (err) { A.toast(err.message, "err"); }
      });
    }

    load();
  }

  A.views.files = async function (root) {
    render(root);
  };
})();
