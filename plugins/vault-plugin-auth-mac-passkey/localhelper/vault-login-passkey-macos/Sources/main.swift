import AppKit
import WebKit
import Network

// MARK: - Common (Vault client + token persistence)

struct VaultLoginConfig {
    var vaultAddr: String
    var mountPath: String
    var userHandle: String
    var userName: String?
    var role: String?
    /// Explicit token for `register/*` (else env `VAULT_TOKEN` or `~/.vault-token`).
    var vaultToken: String?
}

enum VaultLoginPasskeyError: Error, CustomStringConvertible, LocalizedError {
    case invalidArgs(String)
    case invalidVaultAddr
    case missing(String)
    case lookupSelfPermissionDenied

    var description: String {
        switch self {
        case .invalidArgs(let s): return "invalid args: \(s)"
        case .invalidVaultAddr: return "invalid vaultAddr"
        case .missing(let s): return "missing: \(s)"
        case .lookupSelfPermissionDenied:
            return """
            Vault denied auth/token/lookup-self for this token (HTTP 401/403). Passkey-issued tokens usually cannot call that path. \
            Use --user-handle with the entity name or id from registration, or a token whose policy allows path \"auth/token/lookup-self\". \
            CLI login auto-resolve uses only --vault-token / VAULT_TOKEN (not ~/.vault-token). In the UI, only the Vault token field is used — not VAULT_TOKEN.
            """
        }
    }

    var errorDescription: String? { description }
}

final class VaultClient {
    func call(path: String, payload: [String: Any], vaultAddr: String, mountPath: String, vaultToken: String? = nil) async throws -> [String: Any] {
        let addr = vaultAddr.trimmingCharacters(in: .whitespacesAndNewlines)
        var mount = mountPath.trimmingCharacters(in: .whitespacesAndNewlines)
        mount = mount.trimmingCharacters(in: CharacterSet(charactersIn: "/"))

        guard let baseURL = URL(string: addr), baseURL.scheme != nil else {
            throw VaultLoginPasskeyError.invalidVaultAddr
        }

        let url = baseURL.appendingPathComponent("v1").appendingPathComponent(mount).appendingPathComponent(path)
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let t = vaultToken?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty {
            req.setValue(t, forHTTPHeaderField: "X-Vault-Token")
        }
        req.httpBody = try JSONSerialization.data(withJSONObject: payload, options: [])

        let (data, httpResp) = try await URLSession.shared.data(for: req)
        guard let r = httpResp as? HTTPURLResponse else {
            throw NSError(domain: "vault-login-passkey", code: 3, userInfo: [NSLocalizedDescriptionKey: "no HTTP response"])
        }

        let obj = try JSONSerialization.jsonObject(with: data, options: [])
        if r.statusCode >= 200 && r.statusCode < 300 {
            return obj as? [String: Any] ?? ["raw": obj]
        }

        let body: Any = obj
        let bodyString: String
        if let d = try? JSONSerialization.data(withJSONObject: body, options: [.prettyPrinted]),
           let s = String(data: d, encoding: .utf8) {
            bodyString = s
        } else {
            bodyString = String(describing: body)
        }
        throw NSError(
            domain: "vault-login-passkey.http",
            code: r.statusCode,
            userInfo: [
                NSLocalizedDescriptionKey: "Vault API returned HTTP \(r.statusCode) for \(url.path)",
                "httpStatus": r.statusCode,
                "url": url.absoluteString,
                "body": bodyString
            ]
        )
    }
}

/// Passkey `user_handle` the plugin expects (entity name, or entity id if name is empty).
struct ResolvedPasskeyPrincipal: Sendable {
    let userHandle: String
    let entityId: String
    let source: String
}

extension VaultClient {
    func resolvePasskeyPrincipal(vaultAddr: String, vaultToken: String) async throws -> ResolvedPasskeyPrincipal {
        let trimmed = vaultToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw VaultLoginPasskeyError.missing("vault token for principal resolution")
        }

        let lookup: [String: Any]
        do {
            lookup = try await vaultJSONRequest(
                method: "POST",
                v1Path: "auth/token/lookup-self",
                jsonBody: [:],
                vaultAddr: vaultAddr,
                vaultToken: trimmed
            )
        } catch {
            let ne = error as NSError
            let httpStatus =
                (ne.userInfo["httpStatus"] as? NSNumber)?.intValue
                ?? (ne.userInfo["httpStatus"] as? Int)
                ?? (ne.domain == "vault-login-passkey.http" ? ne.code : 0)
            if httpStatus == 403 || httpStatus == 401 {
                throw VaultLoginPasskeyError.lookupSelfPermissionDenied
            }
            throw error
        }
        let data = vaultUnwrapData(lookup)
        guard let entityId = data["entity_id"] as? String, !entityId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw VaultLoginPasskeyError.missing("token has no entity_id (enable Identity on the auth method / use a token bound to an entity)")
        }

        if let ent = try? await vaultJSONRequest(
            method: "GET",
            v1Path: "identity/entity/id/\(entityId)",
            jsonBody: nil,
            vaultAddr: vaultAddr,
            vaultToken: trimmed
        ) {
            let entData = vaultUnwrapData(ent)
            if let name = entData["name"] as? String {
                let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
                if !n.isEmpty {
                    return ResolvedPasskeyPrincipal(userHandle: n, entityId: entityId, source: "entity_name")
                }
            }
        }

        return ResolvedPasskeyPrincipal(userHandle: entityId, entityId: entityId, source: "entity_id")
    }

    private func vaultUnwrapData(_ root: [String: Any]) -> [String: Any] {
        if let d = root["data"] as? [String: Any] { return d }
        return root
    }

    private func vaultJSONRequest(
        method: String,
        v1Path: String,
        jsonBody: [String: Any]?,
        vaultAddr: String,
        vaultToken: String
    ) async throws -> [String: Any] {
        var path = v1Path.trimmingCharacters(in: .whitespacesAndNewlines)
        path = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))

        let base = vaultAddr.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard URL(string: base)?.scheme != nil else {
            throw VaultLoginPasskeyError.invalidVaultAddr
        }

        let urlString = "\(base)/v1/\(path)"
        guard let url = URL(string: urlString) else {
            throw VaultLoginPasskeyError.invalidVaultAddr
        }

        var req = URLRequest(url: url)
        req.httpMethod = method
        let tok = vaultToken.trimmingCharacters(in: .whitespacesAndNewlines)
        req.setValue(tok, forHTTPHeaderField: "X-Vault-Token")
        if let jsonBody {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: jsonBody, options: [])
        }

        let (data, httpResp) = try await URLSession.shared.data(for: req)
        guard let r = httpResp as? HTTPURLResponse else {
            throw NSError(domain: "vault-login-passkey", code: 3, userInfo: [NSLocalizedDescriptionKey: "no HTTP response"])
        }

        let obj = try JSONSerialization.jsonObject(with: data, options: [])
        guard let dict = obj as? [String: Any] else {
            throw NSError(domain: "vault-login-passkey", code: 4, userInfo: [NSLocalizedDescriptionKey: "non-object JSON from Vault"])
        }

        if r.statusCode >= 200 && r.statusCode < 300 {
            return dict
        }

        let bodyString: String
        if let d = try? JSONSerialization.data(withJSONObject: obj, options: [.prettyPrinted]),
           let s = String(data: d, encoding: .utf8) {
            bodyString = s
        } else {
            bodyString = String(describing: obj)
        }
        throw NSError(
            domain: "vault-login-passkey.http",
            code: r.statusCode,
            userInfo: [
                NSLocalizedDescriptionKey: "Vault API returned HTTP \(r.statusCode) for \(url.path)",
                "httpStatus": r.statusCode,
                "url": url.absoluteString,
                "body": bodyString
            ]
        )
    }
}

enum VaultTokenStore {
    static func writeVaultToken(_ token: String) throws {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let tokenPath = home.appendingPathComponent(".vault-token")
        try token.write(to: tokenPath, atomically: true, encoding: .utf8)
    }

    static func readVaultToken() -> String? {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let tokenPath = home.appendingPathComponent(".vault-token")
        guard let data = try? Data(contentsOf: tokenPath),
              let s = String(data: data, encoding: .utf8) else { return nil }
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }

    /// Order: explicit non-empty string, then `VAULT_TOKEN`, then `~/.vault-token`.
    static func resolveVaultToken(explicit: String?) -> String? {
        if let e = explicit?.trimmingCharacters(in: .whitespacesAndNewlines), !e.isEmpty { return e }
        if let env = ProcessInfo.processInfo.environment["VAULT_TOKEN"]?.trimmingCharacters(in: .whitespacesAndNewlines), !env.isEmpty {
            return env
        }
        return readVaultToken()
    }

    /// For passkey *login* principal resolution only: explicit token or `VAULT_TOKEN`, not `~/.vault-token`.
    static func resolveVaultTokenForLoginPrincipal(explicit: String?) -> String? {
        if let e = explicit?.trimmingCharacters(in: .whitespacesAndNewlines), !e.isEmpty { return e }
        if let env = ProcessInfo.processInfo.environment["VAULT_TOKEN"]?.trimmingCharacters(in: .whitespacesAndNewlines), !env.isEmpty {
            return env
        }
        return nil
    }
}

/// `register/finish` Vault JSON may nest `user_handle` under `data` (or deeper); walk the tree.
private func registerFinishUserHandle(_ finish: [String: Any]) -> String? {
    func asTrimmedString(_ v: Any?) -> String? {
        if let s = v as? String {
            let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : t
        }
        if let s = v as? NSString {
            let t = String(s).trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : t
        }
        return nil
    }
    func walk(_ obj: Any, depth: Int) -> String? {
        guard depth < 12 else { return nil }
        if let dict = obj as? [String: Any] {
            if let uh = asTrimmedString(dict["user_handle"]) { return uh }
            for (_, v) in dict {
                if let found = walk(v, depth: depth + 1) { return found }
            }
        } else if let arr = obj as? [Any] {
            for v in arr {
                if let found = walk(v, depth: depth + 1) { return found }
            }
        }
        return nil
    }
    return walk(finish, depth: 0)
}

// MARK: - Persisted HTML form (WK UI has no @AppStorage)

private struct PasskeyUIFormState: Codable {
    var vaultAddr: String
    var mountPath: String
    var userName: String
    var userHandle: String
    var role: String
}

private enum PasskeyUIFormStore {
    static let defaultsKey = "vaultLoginPasskey.uiForm.v1"

    static func save(vaultAddr: String, mountPath: String, userName: String, userHandle: String, role: String) {
        let roleTrim = role.trimmingCharacters(in: .whitespacesAndNewlines)
        let state = PasskeyUIFormState(
            vaultAddr: vaultAddr.trimmingCharacters(in: .whitespacesAndNewlines),
            mountPath: mountPath.trimmingCharacters(in: .whitespacesAndNewlines),
            userName: userName.trimmingCharacters(in: .whitespacesAndNewlines),
            userHandle: userHandle.trimmingCharacters(in: .whitespacesAndNewlines),
            role: roleTrim.isEmpty ? "default" : roleTrim
        )
        guard let data = try? JSONEncoder().encode(state) else { return }
        UserDefaults.standard.set(data, forKey: defaultsKey)
    }

    static func load() -> PasskeyUIFormState? {
        guard let data = UserDefaults.standard.data(forKey: defaultsKey) else { return nil }
        return try? JSONDecoder().decode(PasskeyUIFormState.self, from: data)
    }

    /// JSON object literal for embedding in `evaluateJavaScript` (values only; keys fixed in JS).
    static func jsonObjectLiteralForScript() -> String? {
        guard let s = load() else { return nil }
        let dict: [String: String] = [
            "vaultAddr": s.vaultAddr,
            "mountPath": s.mountPath,
            "userName": s.userName,
            "userHandle": s.userHandle,
            "role": s.role
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: dict, options: []),
              let json = String(data: data, encoding: .utf8) else { return nil }
        return json
    }
}

/// WKWebView text fields need a responder chain + Edit menu for ⌘C / ⌘V; this forwards command-key editing shortcuts.
private final class EditingWKWebView: WKWebView {
    override var acceptsFirstResponder: Bool { true }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        let mod = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        guard mod.contains(.command), let ch = event.charactersIgnoringModifiers?.lowercased() else {
            return super.performKeyEquivalent(with: event)
        }
        if ch == "c" || ch == "v" || ch == "x" || ch == "a" {
            return super.performKeyEquivalent(with: event)
        }
        return super.performKeyEquivalent(with: event)
    }
}

final class VaultLoginPasskeyApp: NSObject, NSApplicationDelegate, WKScriptMessageHandler, WKNavigationDelegate {
    private var window: NSWindow!
    private var webView: EditingWKWebView!
    private var localKeyEventMonitor: Any?
    private let vault = VaultClient()

    func applicationWillFinishLaunching(_ notification: Notification) {
        Self.installMinimalMainMenu(appName: "Vault Passkey Login")
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let contentController = WKUserContentController()
        contentController.add(self, name: "vault")

        let config = WKWebViewConfiguration()
        config.userContentController = contentController

        webView = EditingWKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self
        // Use localhost origin so WebAuthn can work in a secure context without HTTPS during dev.
        // WebAuthn treats http://localhost as a secure context in most user agents.
        webView.loadHTMLString(Self.html, baseURL: URL(string: "http://localhost")!)

        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 900, height: 680),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.center()
        window.title = "Vault Passkey Login"
        window.contentView = webView
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        installLocalEditCommandShortcuts()
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let m = localKeyEventMonitor {
            NSEvent.removeMonitor(m)
            localKeyEventMonitor = nil
        }
    }

    /// WKWebView often does not receive ⌘C / ⌘V from the menu alone; route editing selectors down the responder chain.
    private func installLocalEditCommandShortcuts() {
        localKeyEventMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self else { return event }
            guard self.window.isKeyWindow else { return event }
            let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
            guard flags.contains(.command), !flags.contains(.control) else { return event }
            guard let ch = event.charactersIgnoringModifiers?.lowercased(), ch.count == 1 else { return event }
            let sel: Selector? = switch ch.first! {
            case "c": NSSelectorFromString("copy:")
            case "v": NSSelectorFromString("paste:")
            case "x": NSSelectorFromString("cut:")
            case "a": NSSelectorFromString("selectAll:")
            default: nil
            }
            guard let sel else { return event }
            if NSApp.sendAction(sel, to: nil, from: self.webView) {
                return nil
            }
            return event
        }
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        window.makeFirstResponder(webView)
        applyPersistedFormToWebView(webView)
    }

    private func applyPersistedFormToWebView(_ webView: WKWebView) {
        guard let json = PasskeyUIFormStore.jsonObjectLiteralForScript() else { return }
        let js = """
        (function(){
          try {
            var o = \(json);
            function set(id, v){ var e = document.getElementById(id); if (e) e.value = v != null ? String(v) : ''; }
            set('vaultAddr', o.vaultAddr);
            set('mountPath', o.mountPath);
            set('userName', o.userName);
            set('userHandle', o.userHandle);
            set('role', o.role);
          } catch (e) {}
        })();
        """
        webView.evaluateJavaScript(js, completionHandler: nil)
    }

    /// SwiftPM apps often have no menu bar; without Edit › Paste, ⌘V does not reach WKWebView.
    private static func installMinimalMainMenu(appName: String) {
        let mainMenu = NSMenu()
        let appItem = NSMenuItem()
        mainMenu.addItem(appItem)
        let appMenu = NSMenu()
        appItem.submenu = appMenu
        appMenu.addItem(withTitle: "Quit \(appName)", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

        let editItem = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        mainMenu.addItem(editItem)
        let editMenu = NSMenu(title: "Edit")
        editItem.submenu = editMenu
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        let redo = NSMenuItem(title: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        redo.keyEquivalentModifierMask = [.command, .shift]
        editMenu.addItem(redo)
        editMenu.addItem(.separator())
        let cut = NSMenuItem(title: "Cut", action: NSSelectorFromString("cut:"), keyEquivalent: "x")
        let copy = NSMenuItem(title: "Copy", action: NSSelectorFromString("copy:"), keyEquivalent: "c")
        let paste = NSMenuItem(title: "Paste", action: NSSelectorFromString("paste:"), keyEquivalent: "v")
        let selAll = NSMenuItem(title: "Select All", action: NSSelectorFromString("selectAll:"), keyEquivalent: "a")
        for i in [cut, copy, paste, selAll] {
            i.target = nil
            editMenu.addItem(i)
        }

        NSApp.mainMenu = mainMenu
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return true
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.name == "vault" else { return }
        guard let body = message.body as? [String: Any] else {
            return
        }

        let requestId = body["requestId"] as? String ?? UUID().uuidString
        let action = body["action"] as? String ?? ""
        Task {
            do {
                let result = try await handle(action: action, body: body)
                await sendResult(requestId: requestId, ok: true, result: result)
            } catch {
                let msg = (error as? LocalizedError)?.errorDescription ?? String(describing: error)
                await sendResult(requestId: requestId, ok: false, result: ["error": msg])
            }
        }
    }

    private func handle(action: String, body: [String: Any]) async throws -> [String: Any] {
        switch action {
        case "register":
            return try await handleRegister(body: body)
        case "login":
            return try await handleLogin(body: body)
        default:
            throw NSError(domain: "vault-login-passkey", code: 1, userInfo: [NSLocalizedDescriptionKey: "unknown action: \(action)"])
        }
    }

    private func handleRegister(body: [String: Any]) async throws -> [String: Any] {
        let vaultAddr = body["vaultAddr"] as? String ?? ""
        let mountPath = body["mountPath"] as? String ?? "auth/passkey"
        let userNameOpt = (body["userName"] as? String).flatMap { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0 }
        let roleField = (body["role"] as? String ?? "default").trimmingCharacters(in: .whitespacesAndNewlines)
        let userHandleField = (body["userHandle"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let vaultToken = VaultTokenStore.resolveVaultToken(explicit: body["vaultToken"] as? String)
        guard let vaultToken, !vaultToken.isEmpty else {
            throw NSError(domain: "vault-login-passkey", code: 11, userInfo: [NSLocalizedDescriptionKey: "Vault token required for registration: paste token, set VAULT_TOKEN, or use ~/.vault-token from a prior login"])
        }

        var beginPayload: [String: Any] = [:]
        if let userNameOpt {
            beginPayload["user_name"] = userNameOpt
        }

        let begin = try await vault.call(path: "register/begin", payload: beginPayload, vaultAddr: vaultAddr, mountPath: mountPath, vaultToken: vaultToken)

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 10, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        // AuthenticationServices requires a signed app bundle (application identifier).
        // For a SwiftPM-built helper binary, use Safari-based WebAuthn flow instead.
        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .register, options: optionsAny)
        let credentialB64 = try base64urlJSONBytes(webauthnJSON)
        let finish = try await vault.call(path: "register/finish", payload: [
            "session_id": sessionId,
            "credential": credentialB64
        ], vaultAddr: vaultAddr, mountPath: mountPath, vaultToken: vaultToken)

        var result: [String: Any] = ["begin": begin, "finish": finish]
        let resolvedHandle = registerFinishUserHandle(finish) ?? (userHandleField.isEmpty ? nil : userHandleField)
        if let uh = resolvedHandle {
            result["savedUserHandle"] = uh
        }
        let roleSaved = roleField.isEmpty ? "default" : roleField
        let nameSaved = userNameOpt ?? ""
        let handleStored = resolvedHandle ?? userHandleField
        PasskeyUIFormStore.save(
            vaultAddr: vaultAddr,
            mountPath: mountPath,
            userName: nameSaved,
            userHandle: handleStored,
            role: roleSaved
        )
        result["savedForm"] = [
            "vaultAddr": vaultAddr,
            "mountPath": mountPath,
            "userName": nameSaved,
            "userHandle": handleStored,
            "role": roleSaved
        ]
        return result
    }

    private func handleLogin(body: [String: Any]) async throws -> [String: Any] {
        let vaultAddr = body["vaultAddr"] as? String ?? ""
        let mountPath = body["mountPath"] as? String ?? "auth/passkey"
        var userHandle = (body["userHandle"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let role = body["role"] as? String ?? "default"
        let loginToken = (body["vaultToken"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)

        if userHandle.isEmpty, !loginToken.isEmpty {
            let r = try await vault.resolvePasskeyPrincipal(vaultAddr: vaultAddr, vaultToken: loginToken)
            userHandle = r.userHandle
        }
        guard !userHandle.isEmpty else {
            throw NSError(domain: "vault-login-passkey", code: 21, userInfo: [NSLocalizedDescriptionKey: "login needs user_handle or a Vault token in the field (VAULT_TOKEN is not used for UI login auto-resolve; ~/.vault-token is never used for that)"])
        }

        let begin = try await vault.call(path: "login/begin", payload: [
            "role": role,
            "user_handle": userHandle
        ], vaultAddr: vaultAddr, mountPath: mountPath)

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 20, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .login, options: optionsAny)
        let credentialB64 = try base64urlJSONBytes(webauthnJSON)
        let finish = try await vault.call(path: "login/finish", payload: [
            "session_id": sessionId,
            "credential": credentialB64
        ], vaultAddr: vaultAddr, mountPath: mountPath)

        if let auth = finish["auth"] as? [String: Any],
           let token = auth["client_token"] as? String,
           !token.isEmpty {
            try VaultTokenStore.writeVaultToken(token)
            return ["begin": begin, "finish": finish, "savedToken": true]
        }
        return ["begin": begin, "finish": finish, "savedToken": false]
    }

    private func base64urlJSONBytes(_ obj: [String: Any]) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: obj, options: [])
        return data.base64URLEncodedString()
    }

    @MainActor
    private func sendResult(requestId: String, ok: Bool, result: [String: Any]) async {
        guard let jsonData = try? JSONSerialization.data(withJSONObject: ["requestId": requestId, "ok": ok, "result": result], options: []),
              let json = String(data: jsonData, encoding: .utf8) else {
            return
        }
        let js = "window.__vaultNativeResult(\(json));"
        _ = try? await webView.evaluateJavaScript(js)
    }
}

// MARK: - CLI mode

enum CLIMode {
    case ui
    case cliRegister
    case cliLogin
    case help
}

struct CLI {
    private static func log(_ s: String) {
        let msg = (s + "\n").data(using: .utf8) ?? Data()
        try? FileHandle.standardError.write(contentsOf: msg)
    }

    static func usage() -> String {
        """
Usage:
  vault-login-passkey ui
  vault-login-passkey cli register --vault-addr <addr> --mount-path <path> [--user-name <name>] [--vault-token <tok>]
  vault-login-passkey cli login    --vault-addr <addr> --mount-path <path> [--user-handle <sub>] [--vault-token <tok>] --role <role>

Examples:
  vault-login-passkey ui
  vault-login-passkey cli register --vault-addr http://localhost:8200 --mount-path auth/passkey
  vault-login-passkey cli login --vault-addr http://localhost:8200 --mount-path auth/passkey --role default
  vault-login-passkey cli login --vault-addr http://localhost:8200 --mount-path auth/passkey --user-handle my-entity-name --role default
"""
    }

    static func parse(_ argv: [String]) throws -> (CLIMode, VaultLoginConfig?) {
        // argv includes executable at [0]
        let args = Array(argv.dropFirst())
        if args.isEmpty {
            return (.ui, nil)
        }
        if args.contains("--help") || args.contains("-h") || args.first == "help" {
            return (.help, nil)
        }
        if args.first == "ui" {
            return (.ui, nil)
        }
        guard args.first == "cli" else {
            throw VaultLoginPasskeyError.invalidArgs("expected 'ui' or 'cli'")
        }
        guard args.count >= 2 else {
            throw VaultLoginPasskeyError.invalidArgs("expected 'register' or 'login'")
        }
        let sub = args[1]

        func value(_ flag: String) -> String? {
            guard let idx = args.firstIndex(of: flag), idx + 1 < args.count else { return nil }
            return args[idx + 1]
        }

        let vaultAddr = value("--vault-addr") ?? value("--vault_addr") ?? ""
        let mountPath = value("--mount-path") ?? value("--mount_path") ?? "auth/passkey"
        let userHandle = value("--user-handle") ?? value("--user_handle") ?? ""
        let userName = value("--user-name") ?? value("--user_name")
        let role = value("--role")
        let vaultToken = value("--vault-token") ?? value("--vault_token")

        if vaultAddr.isEmpty { throw VaultLoginPasskeyError.missing("--vault-addr") }

        var cfg = VaultLoginConfig(vaultAddr: vaultAddr, mountPath: mountPath, userHandle: userHandle, userName: userName, role: role, vaultToken: vaultToken)

        switch sub {
        case "register":
            return (.cliRegister, cfg)
        case "login":
            let hasLoginResolveToken = !(VaultTokenStore.resolveVaultTokenForLoginPrincipal(explicit: vaultToken) ?? "").isEmpty
            if userHandle.isEmpty && !hasLoginResolveToken {
                throw VaultLoginPasskeyError.missing("--user-handle or --vault-token / VAULT_TOKEN for lookup-self (~/.vault-token is not used for login auto-resolve)")
            }
            if (cfg.role ?? "").isEmpty { cfg.role = "default" }
            return (.cliLogin, cfg)
        default:
            throw VaultLoginPasskeyError.invalidArgs("unknown cli subcommand: \(sub)")
        }
    }

    static func runRegister(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        let vault = VaultClient()
        guard let token = VaultTokenStore.resolveVaultToken(explicit: cfg.vaultToken), !token.isEmpty else {
            throw VaultLoginPasskeyError.missing("Vault token for registration (VAULT_TOKEN, ~/.vault-token, or --vault-token)")
        }
        log("[passkey] register: calling Vault register/begin ...")
        var beginPayload: [String: Any] = [:]
        if let un = cfg.userName, !un.isEmpty {
            beginPayload["user_name"] = un
        }
        let begin = try await vault.call(path: "register/begin", payload: beginPayload, vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath, vaultToken: token)

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 10, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        log("[passkey] register: opening Safari at http://localhost:8765/ (waiting for approval)...")
        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .register, options: optionsAny)
        log("[passkey] register: received WebAuthn result, calling Vault register/finish ...")
        let credentialB64 = try JSONSerialization.data(withJSONObject: webauthnJSON, options: []).base64URLEncodedString()
        let finish = try await vault.call(path: "register/finish", payload: [
            "session_id": sessionId,
            "credential": credentialB64
        ], vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath, vaultToken: token)

        var result: [String: Any] = ["begin": begin, "finish": finish]
        let uhFromFinish = registerFinishUserHandle(finish)
        let userHandleField = cfg.userHandle.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedHandle = uhFromFinish ?? (userHandleField.isEmpty ? nil : userHandleField)
        if let uh = resolvedHandle {
            result["savedUserHandle"] = uh
        }
        let roleSaved = (cfg.role ?? "default").trimmingCharacters(in: .whitespacesAndNewlines)
        let roleFinal = roleSaved.isEmpty ? "default" : roleSaved
        let nameSaved = cfg.userName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        PasskeyUIFormStore.save(
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath,
            userName: nameSaved,
            userHandle: resolvedHandle ?? userHandleField,
            role: roleFinal
        )
        return result
    }

    static func runLogin(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        let vault = VaultClient()
        var userHandle = cfg.userHandle.trimmingCharacters(in: .whitespacesAndNewlines)
        if userHandle.isEmpty, let t = VaultTokenStore.resolveVaultTokenForLoginPrincipal(explicit: cfg.vaultToken), !t.isEmpty {
            let r = try await vault.resolvePasskeyPrincipal(vaultAddr: cfg.vaultAddr, vaultToken: t)
            userHandle = r.userHandle
            log("[passkey] login: resolved user_handle from token (\(r.source)): \(userHandle)")
        }
        guard !userHandle.isEmpty else {
            throw VaultLoginPasskeyError.missing("--user-handle or --vault-token / VAULT_TOKEN to resolve principal (~/.vault-token is not used for login auto-resolve)")
        }
        log("[passkey] login: calling Vault login/begin ...")
        let begin = try await vault.call(path: "login/begin", payload: [
            "role": cfg.role ?? "default",
            "user_handle": userHandle
        ], vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath)

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 20, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        log("[passkey] login: opening Safari at http://localhost:8765/ (waiting for approval)...")
        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .login, options: optionsAny)
        log("[passkey] login: received WebAuthn result, calling Vault login/finish ...")
        let credentialB64 = try JSONSerialization.data(withJSONObject: webauthnJSON, options: []).base64URLEncodedString()
        let finish = try await vault.call(path: "login/finish", payload: [
            "session_id": sessionId,
            "credential": credentialB64
        ], vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath)

        if let auth = finish["auth"] as? [String: Any],
           let token = auth["client_token"] as? String,
           !token.isEmpty {
            try VaultTokenStore.writeVaultToken(token)
            log("[passkey] login: wrote token to ~/.vault-token")
            return ["begin": begin, "finish": finish, "savedToken": true]
        }
        return ["begin": begin, "finish": finish, "savedToken": false]
    }
}

extension Data {
    init?(base64URLEncoded string: String) {
        var s = string.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        let pad = (4 - (s.count % 4)) % 4
        if pad > 0 { s += String(repeating: "=", count: pad) }
        self.init(base64Encoded: s)
    }

    func base64URLEncodedString() -> String {
        self.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}

// MARK: - Safari-based WebAuthn (works without app identifier)

enum SafariWebAuthnMode {
    case register
    case login
}

enum SafariWebAuthnFlow {
    static func run(mode: SafariWebAuthnMode, options: [String: Any]) async throws -> [String: Any] {
        let server = SafariWebAuthnServer(mode: mode, options: options)
        return try await server.run()
    }
}

final class SafariWebAuthnServer {
    private let mode: SafariWebAuthnMode
    private let options: [String: Any]
    private var listener: NWListener?
    private var resultContinuation: CheckedContinuation<[String: Any], Error>?
    // Keep connection handlers alive until they finish processing.
    private var connectionHandlers: [ObjectIdentifier: HTTPConnectionHandler] = [:]

    init(mode: SafariWebAuthnMode, options: [String: Any]) {
        self.mode = mode
        self.options = options
    }

    func run() async throws -> [String: Any] {
        let port = try await startListener()
        let url = URL(string: "http://localhost:\(port)/")!
        // Sanity check: ensure the listener is reachable from this process.
        // If this fails, Safari will also fail and may show an infinite loading state.
        await selfCheck(port: port)
        NSWorkspace.shared.open(url)
        return try await withCheckedThrowingContinuation { cont in
            self.resultContinuation = cont
        }
    }

    private func selfCheck(port: UInt16) async {
        // Diagnose common "localhost" pitfalls: IPv4/IPv6 resolution mismatch or listener not accepting.
        // We try multiple loopback forms and report which one (if any) succeeds.
        let candidates: [String] = [
            "http://localhost:\(port)/",
            "http://127.0.0.1:\(port)/",
            "http://[::1]:\(port)/",
        ]
        for u in candidates {
            guard let url = URL(string: u) else { continue }
            do {
                var req = URLRequest(url: url)
                req.timeoutInterval = 1.5
                let (_, resp) = try await URLSession.shared.data(for: req)
                let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
                print("[webauthn] self-check OK:", u, "status:", code)
            } catch {
                print("[webauthn] self-check FAILED:", u, String(describing: error))
            }
        }
    }

    private func startListener() async throws -> UInt16 {
        // Prefer a stable port so Vault allowed_origins can be fixed.
        let preferredPort: UInt16 = 8765
        if let p = NWEndpoint.Port(rawValue: preferredPort) {
            let stableParams = NWParameters.tcp
            stableParams.allowLocalEndpointReuse = true
            // NOTE: Do NOT force requiredLocalEndpoint here.
            // On some systems, "localhost" resolves to ::1 first; forcing IPv4-only (or vice versa)
            // makes "http://localhost" hang even though the port appears open.

            if let stable = try? NWListener(using: stableParams, on: p) {
            let listener = stable
            self.listener = listener
            listener.newConnectionHandler = { [weak self] conn in
                print("[webauthn] newConnection:", String(describing: conn.endpoint))
                self?.handle(conn)
            }
            return try await withCheckedThrowingContinuation { cont in
                listener.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        print("[webauthn] listener ready on http://localhost:\(preferredPort)/")
                        cont.resume(returning: preferredPort)
                    case .failed(let err):
                        cont.resume(throwing: err)
                    default:
                        break
                    }
                }
                listener.start(queue: .global())
            }
            }
        }

        let params = NWParameters.tcp
        params.allowLocalEndpointReuse = true
        let listener = try NWListener(using: params, on: .any)
        self.listener = listener

        listener.newConnectionHandler = { [weak self] conn in
            print("[webauthn] newConnection:", String(describing: conn.endpoint))
            self?.handle(conn)
        }

        return try await withCheckedThrowingContinuation { cont in
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    if let port = listener.port?.rawValue {
                        print("[webauthn] listener ready on http://localhost:\(port)/")
                        cont.resume(returning: port)
                    } else {
                        cont.resume(throwing: NSError(domain: "webauthn.server", code: 1, userInfo: [NSLocalizedDescriptionKey: "no port"]))
                    }
                case .failed(let err):
                    cont.resume(throwing: err)
                default:
                    break
                }
            }
            listener.start(queue: .global())
        }
    }

    fileprivate func finish(_ dict: [String: Any]) {
        listener?.cancel()
        resultContinuation?.resume(returning: dict)
        resultContinuation = nil
    }

    fileprivate func fail(_ error: Error) {
        listener?.cancel()
        resultContinuation?.resume(throwing: error)
        resultContinuation = nil
    }

    private func handle(_ conn: NWConnection) {
        let handler = HTTPConnectionHandler(server: self, conn: conn)
        let key = ObjectIdentifier(handler)
        connectionHandlers[key] = handler
        handler.start()
    }

    fileprivate func dropHandler(_ handler: HTTPConnectionHandler) {
        connectionHandlers.removeValue(forKey: ObjectIdentifier(handler))
    }

    fileprivate func respond(_ conn: NWConnection, status: String, contentType: String, body: Data) {
        // Build a strict HTTP/1.1 response header. Do NOT use Swift multiline strings here,
        // because they may insert extra LF characters and break Safari's parser.
        let header = "HTTP/1.1 \(status)\r\n" +
            "Content-Type: \(contentType)\r\n" +
            "Content-Length: \(body.count)\r\n" +
            "Connection: close\r\n" +
            "\r\n"
        var out = Data(header.utf8)
        out.append(body)
        conn.send(content: out, completion: .contentProcessed { _ in
            conn.cancel()
        })
    }

    fileprivate func pageHTML() -> String {
        let optionsJSON: String = (try? JSONSerialization.data(withJSONObject: options, options: []))
            .flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
        let isRegister = (mode == .register)
        return """
<!doctype html>
<html>
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vault Passkey</title></head>
<body>
<h3>Vault Passkey</h3>
<button id="go" style="padding:10px 12px;font-size:14px;">Continue (trigger Passkey)</button>
<pre id="out">Ready. Click the button to continue.</pre>
<script>
const options = \(optionsJSON);
function b64urlToBuf(b64url) {
  if (!b64url || typeof b64url !== 'string') return null;
  try {
    const pad = '='.repeat((4 - (b64url.length % 4)) % 4);
    const b64 = (b64url + pad).replace(/-/g,'+').replace(/_/g,'/');
    const str = atob(b64);
    const bytes = new Uint8Array(str.length);
    for (let i=0;i<str.length;i++) bytes[i] = str.charCodeAt(i);
    return bytes.buffer;
  } catch (e) { return null; }
}
function normalizeUserId(id) {
  if (id === undefined || id === null) throw new TypeError('user.id is missing');
  let buf = null;
  if (typeof id === 'string') {
    buf = b64urlToBuf(id);
    if (buf && (buf.byteLength < 1 || buf.byteLength > 64)) buf = null;
    if (!buf) {
      const te = new TextEncoder();
      const u = te.encode(id);
      if (u.length >= 1 && u.length <= 64) buf = u.buffer;
    }
  } else if (Array.isArray(id)) {
    const u = new Uint8Array(id);
    if (u.length >= 1 && u.length <= 64) buf = u.buffer;
  } else if (id instanceof ArrayBuffer) {
    if (id.byteLength >= 1 && id.byteLength <= 64) buf = id;
  } else if (id instanceof Uint8Array) {
    if (id.length >= 1 && id.length <= 64) buf = id.buffer;
  }
  if (!buf) throw new TypeError('user.id must be 1-64 bytes after normalization');
  return buf;
}
function bufToB64url(buf) {
  const bytes = new Uint8Array(buf);
  let str = '';
  for (const b of bytes) str += String.fromCharCode(b);
  return btoa(str).replace(/\\+/g,'-').replace(/\\//g,'_').replace(/=+$/,'');
}
function publicKeyCredentialToJSON(cred) {
  if (cred instanceof ArrayBuffer) return bufToB64url(cred);
  if (cred instanceof Uint8Array) return bufToB64url(cred.buffer);
  if (Array.isArray(cred)) return cred.map(publicKeyCredentialToJSON);
  if (cred && typeof cred === 'object') {
    const obj = {};
    for (const k in cred) obj[k] = publicKeyCredentialToJSON(cred[k]);
    return obj;
  }
  return cred;
}
function serializeError(e) {
  if (!e) return { message: "unknown error" };
  if (typeof e === 'string') return { message: e };
  return {
    name: e.name,
    message: e.message,
    stack: e.stack
  };
}
function credentialToJSON(cred) {
  // Prefer the built-in instance method when available.
  // Some Safari builds throw if toJSON is invoked without the proper receiver.
  try {
    if (cred && typeof cred.toJSON === 'function') {
      return cred.toJSON();
    }
  } catch (e) {
    // fall through to manual conversion
    console.warn('cred.toJSON() failed:', e);
  }
  return publicKeyCredentialToJSON(cred);
}
async function main() {
  document.getElementById('out').textContent = 'Starting WebAuthn...';
  const pk = options.publicKey || options;
  pk.challenge = b64urlToBuf(pk.challenge);
  if (pk.user) pk.user.id = normalizeUserId(pk.user.id);
  if (pk.excludeCredentials) pk.excludeCredentials = pk.excludeCredentials.map(d => ({...d, id: (typeof d.id==='string') ? b64urlToBuf(d.id) : d.id}));
  if (pk.allowCredentials) pk.allowCredentials = pk.allowCredentials.map(d => ({...d, id: (typeof d.id==='string') ? b64urlToBuf(d.id) : d.id}));
  let cred;
  if (\(isRegister ? "true" : "false")) {
    cred = await navigator.credentials.create({ publicKey: pk });
  } else {
    cred = await navigator.credentials.get({ publicKey: pk });
  }
  const json = publicKeyCredentialToJSON(credentialToJSON(cred));
  document.getElementById('out').textContent = JSON.stringify(json, null, 2);
  try {
    await fetch('/result', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(json) });
    document.getElementById('out').textContent = 'Done. You can close this tab/window.';
    try { window.close(); } catch {}
  } catch (e) {
    document.getElementById('out').textContent = JSON.stringify({ error: serializeError(e) }, null, 2);
  }
}

document.getElementById('go').addEventListener('click', () => {
  main().catch(e => {
    document.getElementById('out').textContent = JSON.stringify({ error: serializeError(e) }, null, 2);
  });
});
</script>
</body></html>
"""
    }
}

final class HTTPConnectionHandler {
    private weak var server: SafariWebAuthnServer?
    private let conn: NWConnection
    private var buffer = Data()
    private var finished = false

    init(server: SafariWebAuthnServer, conn: NWConnection) {
        self.server = server
        self.conn = conn
    }

    func start() {
        print("[webauthn] conn.start")
        conn.start(queue: .global())
        receiveMore()
    }

    private func receiveMore() {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, err in
            guard let self else { return }
            if let err {
                self.conn.cancel()
                self.server?.fail(err)
                self.finish()
                return
            }
            if let data {
                self.buffer.append(data)
            }
            if isComplete {
                _ = self.processIfPossible(force: true)
                self.finish()
                return
            }
            if self.processIfPossible(force: false) {
                self.finish()
                return
            }
            self.receiveMore()
        }
    }

    private func finish() {
        if finished { return }
        finished = true
        server?.dropHandler(self)
    }

    @discardableResult
    private func processIfPossible(force: Bool) -> Bool {
        guard let server else { return true }

        guard let headerRange = buffer.range(of: Data("\r\n\r\n".utf8)) else {
            if force {
                conn.cancel()
                server.fail(NSError(domain: "webauthn.server", code: 10, userInfo: [NSLocalizedDescriptionKey: "incomplete headers"]))
                return true
            }
            return false
        }

        let headerData = buffer.subdata(in: buffer.startIndex..<headerRange.lowerBound)
        guard let headerStr = String(data: headerData, encoding: .utf8) else {
            conn.cancel()
            server.fail(NSError(domain: "webauthn.server", code: 11, userInfo: [NSLocalizedDescriptionKey: "invalid header encoding"]))
            return true
        }

        let lines = headerStr.components(separatedBy: "\r\n")
        guard let requestLine = lines.first else {
            conn.cancel()
            server.fail(NSError(domain: "webauthn.server", code: 12, userInfo: [NSLocalizedDescriptionKey: "missing request line"]))
            return true
        }
        print("[webauthn] request:", requestLine)

        var contentLength = 0
        for line in lines.dropFirst() {
            let lower = line.lowercased()
            if lower.hasPrefix("content-length:") {
                let v = line.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: true).last.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
                contentLength = Int(v) ?? 0
            }
        }
        if contentLength > 0 {
            print("[webauthn] content-length:", contentLength)
        }

        let bodyStart = headerRange.upperBound
        let needed = bodyStart + contentLength
        if buffer.count < needed {
            if force {
                conn.cancel()
                server.fail(NSError(domain: "webauthn.server", code: 13, userInfo: [NSLocalizedDescriptionKey: "incomplete body"]))
                return true
            }
            return false
        }

        let body = buffer.subdata(in: bodyStart..<needed)

        if requestLine.hasPrefix("GET / ") || requestLine.hasPrefix("GET /?") || requestLine.hasPrefix("GET / HTTP/") {
            print("[webauthn] serving page")
            let html = server.pageHTML()
            server.respond(conn, status: "200 OK", contentType: "text/html; charset=utf-8", body: Data(html.utf8))
            return true
        }

        // Browsers often request favicon/apple-touch-icon; answer quickly to avoid noisy UI errors.
        if requestLine.hasPrefix("GET /favicon.ico ") || requestLine.hasPrefix("GET /favicon.ico HTTP/") ||
            requestLine.hasPrefix("GET /apple-touch-icon") {
            server.respond(conn, status: "204 No Content", contentType: "text/plain; charset=utf-8", body: Data())
            return true
        }

        if requestLine.hasPrefix("POST /result ") || requestLine.hasPrefix("POST /result HTTP/") {
            if let obj = try? JSONSerialization.jsonObject(with: body, options: []),
               let dict = obj as? [String: Any] {
                print("[webauthn] received result keys:", Array(dict.keys).sorted())
                server.respond(conn, status: "200 OK", contentType: "application/json", body: Data("{\"ok\":true}".utf8))
                server.finish(dict)
                return true
            }
            print("[webauthn] bad json body (bytes):", body.count)
            server.respond(conn, status: "400 Bad Request", contentType: "text/plain; charset=utf-8", body: Data("bad json".utf8))
            return true
        }

        print("[webauthn] 404 for request:", requestLine)
        server.respond(conn, status: "404 Not Found", contentType: "text/plain; charset=utf-8", body: Data("not found".utf8))
        return true
    }
}

@main
struct Main {
    static func main() async {
        do {
            let (mode, cfg) = try CLI.parse(CommandLine.arguments)
            switch mode {
            case .help:
                print(CLI.usage())
                return
            case .ui:
                let app = NSApplication.shared
                let delegate = VaultLoginPasskeyApp()
                app.setActivationPolicy(.regular)
                app.delegate = delegate
                app.run()
            case .cliRegister:
                guard let cfg else { throw VaultLoginPasskeyError.invalidArgs("missing config") }
                do {
                    let result = try await CLI.runRegister(cfg)
                    let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted])
                    print(String(data: data, encoding: .utf8) ?? "\(result)")
                } catch {
                    fputs("Error: \(error)\n", stderr)
                    exit(1)
                }
            case .cliLogin:
                guard let cfg else { throw VaultLoginPasskeyError.invalidArgs("missing config") }
                do {
                    let result = try await CLI.runLogin(cfg)
                    let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted])
                    print(String(data: data, encoding: .utf8) ?? "\(result)")
                } catch {
                    fputs("Error: \(error)\n", stderr)
                    exit(1)
                }
            }
        } catch {
            fputs("Error: \(error)\n\n", stderr)
            fputs(CLI.usage() + "\n", stderr)
            exit(2)
        }
    }
}

extension VaultLoginPasskeyApp {
    static let html = """
<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Vault Passkey Login</title>
  <style>
    body { font-family: -apple-system, system-ui, sans-serif; margin: 20px; }
    .row { display: flex; gap: 12px; }
    .col { flex: 1; }
    label { display: block; font-size: 12px; color: #444; margin-top: 10px; }
    input { width: 100%; padding: 8px; font-size: 13px; }
    button { padding: 10px 12px; margin-top: 12px; }
    pre { background: #f4f4f4; padding: 12px; height: 240px; overflow: auto; }
  </style>
</head>
<body>
  <h2>Vault Passkey (Touch ID) Login</h2>
  <div class="row">
    <div class="col">
      <label>Vault address</label>
      <input id="vaultAddr" placeholder="https://vault.example.com" />
    </div>
    <div class="col">
      <label>Mount path</label>
      <input id="mountPath" placeholder="auth/passkey" value="auth/passkey" />
    </div>
  </div>

  <div class="row">
    <div class="col">
      <label>Vault token</label>
      <input id="vaultToken" type="password" placeholder="register: required. login: optional (field only — lookup-self; clear if passkey token)" autocomplete="off" />
    </div>
    <div class="col">
      <label>User name (optional, WebAuthn display; register)</label>
      <input id="userName" placeholder="defaults to entity name / id" />
    </div>
  </div>

  <div class="row">
    <div class="col">
      <label>user_handle (login; filled automatically after Register)</label>
      <input id="userHandle" placeholder="entity name or id — saved when registration succeeds" />
    </div>
    <div class="col">
      <label>Role (for login)</label>
      <input id="role" placeholder="default" value="default" />
    </div>
  </div>

  <button onclick="register()">Register passkey</button>
  <button onclick="login()">Login (writes ~/.vault-token)</button>

  <h3>Output</h3>
  <pre id="out"></pre>

  <script>
    const pending = new Map();
    window.__vaultNativeResult = (msg) => {
      const { requestId, ok, result } = msg;
      const p = pending.get(requestId);
      if (p) {
        pending.delete(requestId);
        ok ? p.resolve(result) : p.reject(result);
      }
    };

    function callNative(action, payload) {
      const requestId = crypto.randomUUID();
      return new Promise((resolve, reject) => {
        pending.set(requestId, { resolve, reject });
        window.webkit.messageHandlers.vault.postMessage({ requestId, action, ...payload });
      });
    }

    function out(obj) {
      document.getElementById('out').textContent = JSON.stringify(obj, null, 2);
    }

    function serializeError(e) {
      if (!e) return { message: "unknown error" };
      if (typeof e === 'string') return { message: e };
      if (e instanceof Error) {
        return {
          name: e.name,
          message: e.message,
          stack: e.stack
        };
      }
      try {
        return JSON.parse(JSON.stringify(e));
      } catch {
        return { message: String(e) };
      }
    }

    window.addEventListener('unhandledrejection', (ev) => {
      out({ unhandledRejection: serializeError(ev.reason) });
    });
    window.addEventListener('error', (ev) => {
      out({ windowError: { message: ev.message, filename: ev.filename, lineno: ev.lineno, colno: ev.colno } });
    });

    function bufToBase64url(buf) {
      const bytes = new Uint8Array(buf);
      let str = '';
      for (const b of bytes) str += String.fromCharCode(b);
      const b64 = btoa(str).replace(/\\+/g, '-').replace(/\\//g, '_').replace(/=+$/,'');
      return b64;
    }

    function base64urlToBuf(b64url) {
      if (!b64url || typeof b64url !== 'string') return null;
      try {
        const pad = '='.repeat((4 - (b64url.length % 4)) % 4);
        const b64 = (b64url + pad).replace(/-/g, '+').replace(/_/g, '/');
        const str = atob(b64);
        const bytes = new Uint8Array(str.length);
        for (let i = 0; i < str.length; i++) bytes[i] = str.charCodeAt(i);
        return bytes.buffer;
      } catch (e) { return null; }
    }
    function normalizeUserId(id) {
      if (id === undefined || id === null) throw new TypeError('user.id is missing');
      let buf = null;
      if (typeof id === 'string') {
        buf = base64urlToBuf(id);
        if (buf && (buf.byteLength < 1 || buf.byteLength > 64)) buf = null;
        if (!buf) {
          const te = new TextEncoder();
          const u = te.encode(id);
          if (u.length >= 1 && u.length <= 64) buf = u.buffer;
        }
      } else if (Array.isArray(id)) {
        const u = new Uint8Array(id);
        if (u.length >= 1 && u.length <= 64) buf = u.buffer;
      } else if (id instanceof ArrayBuffer) {
        if (id.byteLength >= 1 && id.byteLength <= 64) buf = id;
      } else if (id instanceof Uint8Array) {
        if (id.length >= 1 && id.length <= 64) buf = id.buffer;
      }
      if (!buf) throw new TypeError('user.id must be 1-64 bytes after normalization');
      return buf;
    }

    function preflightWebAuthn() {
      if (!window.PublicKeyCredential || !navigator.credentials) {
        throw new Error('WebAuthn not available in this WebView/runtime');
      }
      if (location.hostname !== 'localhost') {
        // For dev, we intentionally run under localhost origin so WebAuthn can work without HTTPS.
        // If this changes, registration/login will likely fail with NotAllowedError.
        console.warn('WebAuthn origin is not localhost:', location.origin);
      }
    }

    function normalizeCreateOptions(publicKey) {
      const pk = structuredClone(publicKey);
      // WebAuthn expects ArrayBuffer for challenge and user.id and credential IDs.
      pk.challenge = base64urlToBuf(pk.challenge);
      if (pk.user) {
        pk.user.id = normalizeUserId(pk.user.id);
      }
      if (Array.isArray(pk.excludeCredentials)) {
        pk.excludeCredentials = pk.excludeCredentials.map(d => ({
          ...d,
          id: (typeof d.id === 'string') ? base64urlToBuf(d.id) : d.id
        }));
      }
      return pk;
    }

    function normalizeGetOptions(publicKey) {
      const pk = structuredClone(publicKey);
      pk.challenge = base64urlToBuf(pk.challenge);
      if (Array.isArray(pk.allowCredentials)) {
        pk.allowCredentials = pk.allowCredentials.map(d => ({
          ...d,
          id: (typeof d.id === 'string') ? base64urlToBuf(d.id) : d.id
        }));
      }
      return pk;
    }

    function publicKeyCredentialToJSON(cred) {
      if (cred instanceof ArrayBuffer) return bufToBase64url(cred);
      if (cred instanceof Uint8Array) return bufToBase64url(cred.buffer);
      if (Array.isArray(cred)) return cred.map(x => publicKeyCredentialToJSON(x));
      if (cred && typeof cred === 'object') {
        const obj = {};
        for (const k in cred) obj[k] = publicKeyCredentialToJSON(cred[k]);
        return obj;
      }
      return cred;
    }

    function applySavedForm(f) {
      if (!f || typeof f !== 'object') return;
      const set = (id, k) => {
        const e = document.getElementById(id);
        if (e && f[k] != null && f[k] !== undefined) e.value = String(f[k]);
      };
      set('vaultAddr', 'vaultAddr');
      set('mountPath', 'mountPath');
      set('userName', 'userName');
      set('userHandle', 'userHandle');
      set('role', 'role');
    }

    async function register() {
      try {
        const vaultAddr = document.getElementById('vaultAddr').value;
        const mountPath = document.getElementById('mountPath').value;
        const userName = document.getElementById('userName').value;
        const vaultToken = document.getElementById('vaultToken').value;
        const userHandle = document.getElementById('userHandle').value;
        const role = document.getElementById('role').value;

        const result = await callNative('register', { vaultAddr, mountPath, userName, vaultToken, userHandle, role });
        if (result && result.savedForm) applySavedForm(result.savedForm);
        else if (result && result.savedUserHandle) document.getElementById('userHandle').value = result.savedUserHandle;
        out(result);
      } catch (e) {
        out({ error: serializeError(e) });
      }
    }

    async function login() {
      try {
        const vaultAddr = document.getElementById('vaultAddr').value;
        const mountPath = document.getElementById('mountPath').value;
        const userHandle = document.getElementById('userHandle').value;
        const vaultToken = document.getElementById('vaultToken').value;
        const role = document.getElementById('role').value;

        const result = await callNative('login', { vaultAddr, mountPath, role, userHandle, vaultToken });
        out(result);
      } catch (e) {
        out({ error: serializeError(e) });
      }
    }
  </script>
</body>
</html>
"""
}

