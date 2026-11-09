// AnyWhere 工作站 · 桌面外壳
//
// 只做三件事，界面完全交给控制台本身（保持既有设计，不再叠一层原生侧边栏）：
//   1. 起服务：探测 127.0.0.1:7788，没有就用包内的 anywhered 拉起；
//   2. 起工作站：连上后若处于 stopped 则自动启动；
//   3. 提供窗口与菜单：深链跳转、生命周期、受控浏览器。

import AppKit
import WebKit

let baseURL = ProcessInfo.processInfo.environment["ANYWHERE_BASE"] ?? "http://127.0.0.1:7788"

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var pollTimer: Timer?
    private var daemon: Process?
    private var state = "unknown"
    /// 通过 vmbra:// 深链打开、但控制台还没加载完时，先存在这里。
    private var pendingImport: String?

    /// 快捷跳转项：菜单栏与 ⌘1~⌘4 使用，直接落到控制台对应面板。
    private let jumps: [(key: String, title: String, hash: String, combo: String)] = [
        ("terminal", "终端", "#/terminal", "1"),
        ("desktop", "桌面", "#/desktop", "2"),
        ("network", "网络", "#/network", "3"),
        ("files", "文件", "#/files", "4"),
    ]

    private lazy var token: String = {
        let path = NSString(string: "~/.anywhere/token").expandingTildeInPath
        let raw = (try? String(contentsOfFile: path, encoding: .utf8)) ?? ""
        return raw.trimmingCharacters(in: .whitespacesAndNewlines)
    }()

    // MARK: - 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        buildMenu()
        buildWindow()

        ensureDaemon { [weak self] ok in
            guard let self = self else { return }
            guard ok else {
                self.setTitle("管理服务未启动")
                return
            }
            self.loadConsole()
            if let payload = self.pendingImport {
                self.pendingImport = nil
                self.navigate("/settings?import=" + payload)
            }
            self.autoStartIfStopped()
            self.startPolling()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    /// 开通链接：vmbra://import/<payload> 打开客户端时，直接跳到导入界面。
    func application(_ application: NSApplication, open urls: [URL]) {
        guard let url = urls.last, url.scheme == "vmbra" else { return }
        var payload = url.absoluteString
        if let range = payload.range(of: "vmbra://import/") {
            payload = String(payload[range.upperBound...])
        }
        if let cut = payload.firstIndex(where: { $0 == "?" || $0 == "#" }) {
            payload = String(payload[..<cut])
        }
        guard !payload.isEmpty else { return }
        if window == nil {
            pendingImport = payload
            return
        }
        navigate("/settings?import=" + payload)
    }

    func applicationWillTerminate(_ notification: Notification) {
        pollTimer?.invalidate()
    }

    // MARK: - 窗口

    private func buildWindow() {
        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1220, height: 840),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AnyWhere 工作站"
        window.setFrameAutosaveName("AnyWhereDesktopWindow")
        window.center()
        window.minSize = NSSize(width: 960, height: 620)

        let config = WKWebViewConfiguration()
        webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self
        webView.translatesAutoresizingMaskIntoConstraints = false
        if #available(macOS 12.0, *) {
            webView.underPageBackgroundColor = .windowBackgroundColor
        }
        window.contentView = webView
        NSLayoutConstraint.activate([
            webView.topAnchor.constraint(equalTo: window.contentView!.topAnchor),
            webView.leadingAnchor.constraint(equalTo: window.contentView!.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: window.contentView!.trailingAnchor),
            webView.bottomAnchor.constraint(equalTo: window.contentView!.bottomAnchor),
        ])
        window.makeKeyAndOrderFront(nil)
    }

    private func setTitle(_ suffix: String) {
        window.title = "AnyWhere 工作站" + (suffix.isEmpty ? "" : " · " + suffix)
    }

    // MARK: - 菜单

    private func buildMenu() {
        let main = NSMenu()

        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 AnyWhere 工作站", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "隐藏", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "退出", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        main.addItem(appItem)

        let wsItem = NSMenuItem()
        let wsMenu = NSMenu(title: "工作站")
        addSelfItem(wsMenu, "启动工作站", #selector(startWorkstation), "s")
        addSelfItem(wsMenu, "停止工作站", #selector(stopWorkstation), ".")
        addSelfItem(wsMenu, "重启工作站", #selector(restartWorkstation), "")
        wsMenu.addItem(.separator())
        addSelfItem(wsMenu, "打开受控浏览器", #selector(openBrowser), "b")
        addSelfItem(wsMenu, "在受控 Chrome 中打开控制台", #selector(openConsoleInChrome), "o")
        wsMenu.addItem(.separator())
        addSelfItem(wsMenu, "刷新", #selector(reloadConsole), "r")
        wsItem.submenu = wsMenu
        main.addItem(wsItem)

        let jumpItem = NSMenuItem()
        let jumpMenu = NSMenu(title: "快捷访问")
        for j in jumps {
            let item = NSMenuItem(title: j.title, action: #selector(jumpFromMenu(_:)), keyEquivalent: j.combo)
            item.target = self
            item.representedObject = j.hash
            jumpMenu.addItem(item)
        }
        jumpItem.submenu = jumpMenu
        main.addItem(jumpItem)

        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "撤销", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "重做", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "拷贝", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu
        main.addItem(editItem)

        NSApp.mainMenu = main
    }

    /// 菜单项默认走响应链，AppDelegate 不在链上，必须显式指定 target。
    private func addSelfItem(_ menu: NSMenu, _ title: String, _ action: Selector, _ key: String) {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.target = self
        menu.addItem(item)
    }

    @objc private func jumpFromMenu(_ sender: NSMenuItem) {
        guard let hash = sender.representedObject as? String else { return }
        navigate(hash)
    }

    // MARK: - 控制台

    private func loadConsole(hash: String = "") {
        guard let url = URL(string: baseURL + "/?token=" + token + hash) else { return }
        webView.load(URLRequest(url: url))
    }

    @objc private func reloadConsole() {
        webView.reload()
    }

    private func navigate(_ hash: String) {
        ensureRunning { [weak self] in
            guard let self = self else { return }
            self.webView.evaluateJavaScript("location.hash = '\(hash)'") { _, error in
                if error != nil { self.loadConsole(hash: hash) }
            }
        }
    }

    @objc private func openConsoleInChrome() {
        let target = baseURL + "/?token=" + token
        api("POST", "/api/browser/open", body: ["url": target]) { [weak self] object in
            if let error = object?["error"] as? String {
                self?.setTitle("打开 Chrome 失败：" + error)
            }
        }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        setTitle("控制台不可达")
    }

    // MARK: - 管理服务

    private func api(_ method: String, _ path: String, body: [String: Any]? = nil, _ done: (([String: Any]?) -> Void)? = nil) {
        guard let url = URL(string: baseURL + path) else { done?(nil); return }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 8
        request.setValue(token, forHTTPHeaderField: "X-Anywhere-Token")
        if let body = body, let payload = try? JSONSerialization.data(withJSONObject: body) {
            request.httpBody = payload
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        URLSession.shared.dataTask(with: request) { data, _, _ in
            var object: [String: Any]?
            if let data = data, !data.isEmpty {
                object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
            }
            DispatchQueue.main.async { done?(object) }
        }.resume()
    }

    private func ensureDaemon(_ done: @escaping (Bool) -> Void) {
        api("GET", "/api/status") { [weak self] object in
            if object != nil { done(true); return }
            guard let self = self, let binary = self.daemonBinary() else { done(false); return }
            self.spawn(binary)
            self.waitForDaemon(deadline: Date().addingTimeInterval(20), done: done)
        }
    }

    private func waitForDaemon(deadline: Date, done: @escaping (Bool) -> Void) {
        if Date() > deadline { done(false); return }
        api("GET", "/api/status") { [weak self] object in
            if object != nil { done(true); return }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
                self?.waitForDaemon(deadline: deadline, done: done)
            }
        }
    }

    private func daemonBinary() -> String? {
        var candidates: [String] = []
        if let env = ProcessInfo.processInfo.environment["ANYWHERE_BIN"] { candidates.append(env) }
        if let res = Bundle.main.resourcePath { candidates.append(res + "/anywhered") }
        candidates.append(NSString(string: "~/.anywhere/bin/anywhered").expandingTildeInPath)
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    private func spawn(_ binary: String) {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: binary)
        let logDir = NSString(string: "~/.anywhere/logs").expandingTildeInPath
        try? FileManager.default.createDirectory(atPath: logDir, withIntermediateDirectories: true)
        let logPath = logDir + "/desktop-launch.log"
        if !FileManager.default.fileExists(atPath: logPath) {
            FileManager.default.createFile(atPath: logPath, contents: nil)
        }
        if let handle = FileHandle(forWritingAtPath: logPath) {
            handle.seekToEndOfFile()
            task.standardOutput = handle
            task.standardError = handle
        }
        do {
            try task.run()
            daemon = task
        } catch {
            setTitle("无法启动管理服务")
        }
    }

    // MARK: - 工作站状态

    private func startPolling() {
        pollTimer?.invalidate()
        pollTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        refresh()
    }

    private func refresh() {
        api("GET", "/api/status") { [weak self] object in
            guard let self = self else { return }
            guard
                let ws = object?["workstation"] as? [String: Any],
                let value = ws["state"] as? String
            else {
                self.state = "unknown"
                self.setTitle("未连接")
                return
            }
            self.state = value
            let name = ws["name"] as? String ?? "workstation"
            switch value {
            case "running": self.setTitle("\(name) 运行中")
            case "starting", "stopping": self.setTitle("\(name) 切换中")
            case "error": self.setTitle("\(name) 异常")
            default: self.setTitle("\(name) 已停止")
            }
        }
    }

    private func ensureRunning(_ done: @escaping () -> Void) {
        api("GET", "/api/status") { [weak self] object in
            guard let self = self else { return }
            let value = (object?["workstation"] as? [String: Any])?["state"] as? String ?? ""
            if value == "running" { done(); return }
            self.setTitle("正在启动工作站")
            self.api("POST", "/api/workstation/start") { _ in
                self.refresh()
                done()
            }
        }
    }

    private func autoStartIfStopped() {
        api("GET", "/api/status") { [weak self] object in
            guard
                let self = self,
                let ws = object?["workstation"] as? [String: Any],
                let value = ws["state"] as? String,
                value == "stopped"
            else { return }
            self.setTitle("正在启动工作站")
            self.api("POST", "/api/workstation/start") { _ in self.refresh() }
        }
    }

    @objc private func startWorkstation() {
        setTitle("正在启动工作站")
        api("POST", "/api/workstation/start") { [weak self] _ in self?.refresh() }
    }

    @objc private func stopWorkstation() {
        setTitle("正在停止工作站")
        api("POST", "/api/workstation/stop") { [weak self] _ in self?.refresh() }
    }

    @objc private func restartWorkstation() {
        setTitle("正在重启工作站")
        api("POST", "/api/workstation/restart") { [weak self] _ in self?.refresh() }
    }

    @objc private func openBrowser() {
        api("POST", "/api/browser/open") { [weak self] object in
            if let warning = object?["warning"] as? String, !warning.isEmpty {
                self?.setTitle(warning)
            }
        }
    }
}

// 菜单项的 representedObject 需要在构建时写入目标 hash。
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
