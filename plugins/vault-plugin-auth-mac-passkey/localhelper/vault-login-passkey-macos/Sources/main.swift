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
}

enum VaultLoginPasskeyError: Error, CustomStringConvertible {
    case invalidArgs(String)
    case invalidVaultAddr
    case missing(String)

    var description: String {
        switch self {
        case .invalidArgs(let s): return "invalid args: \(s)"
        case .invalidVaultAddr: return "invalid vaultAddr"
        case .missing(let s): return "missing: \(s)"
        }
    }
}

final class VaultClient {
    func call(path: String, payload: [String: Any], vaultAddr: String, mountPath: String) async throws -> [String: Any] {
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

enum VaultTokenStore {
    static func writeVaultToken(_ token: String) throws {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let tokenPath = home.appendingPathComponent(".vault-token")
        try token.write(to: tokenPath, atomically: true, encoding: .utf8)
    }
}

final class VaultLoginPasskeyApp: NSObject, NSApplicationDelegate, WKScriptMessageHandler {
    private var window: NSWindow!
    private var webView: WKWebView!
    private let vault = VaultClient()

    func applicationDidFinishLaunching(_ notification: Notification) {
        let contentController = WKUserContentController()
        contentController.add(self, name: "vault")

        let config = WKWebViewConfiguration()
        config.userContentController = contentController

        webView = WKWebView(frame: .zero, configuration: config)
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
                await sendResult(requestId: requestId, ok: false, result: ["error": String(describing: error)])
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
        let userHandle = body["userHandle"] as? String ?? ""
        let userName = (body["userName"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? userHandle

        let begin = try await vault.call(path: "register/begin", payload: [
            "user_handle": userHandle,
            "user_name": userName
        ], vaultAddr: vaultAddr, mountPath: mountPath)

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
        ], vaultAddr: vaultAddr, mountPath: mountPath)

        return ["begin": begin, "finish": finish]
    }

    private func handleLogin(body: [String: Any]) async throws -> [String: Any] {
        let vaultAddr = body["vaultAddr"] as? String ?? ""
        let mountPath = body["mountPath"] as? String ?? "auth/passkey"
        let userHandle = body["userHandle"] as? String ?? ""
        let role = body["role"] as? String ?? "default"

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
  vault-login-passkey cli register --vault-addr <addr> --mount-path <path> --user-handle <sub> [--user-name <name>]
  vault-login-passkey cli login    --vault-addr <addr> --mount-path <path> --user-handle <sub> --role <role>

Examples:
  vault-login-passkey ui
  vault-login-passkey cli register --vault-addr http://localhost:8200 --mount-path auth/passkey --user-handle gs.lee
  vault-login-passkey cli login --vault-addr http://localhost:8200 --mount-path auth/passkey --user-handle gs.lee --role default
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

        if vaultAddr.isEmpty { throw VaultLoginPasskeyError.missing("--vault-addr") }
        if userHandle.isEmpty { throw VaultLoginPasskeyError.missing("--user-handle") }

        var cfg = VaultLoginConfig(vaultAddr: vaultAddr, mountPath: mountPath, userHandle: userHandle, userName: userName, role: role)

        switch sub {
        case "register":
            return (.cliRegister, cfg)
        case "login":
            if (cfg.role ?? "").isEmpty { cfg.role = "default" }
            return (.cliLogin, cfg)
        default:
            throw VaultLoginPasskeyError.invalidArgs("unknown cli subcommand: \(sub)")
        }
    }

    static func runRegister(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        let vault = VaultClient()
        log("[passkey] register: calling Vault register/begin ...")
        let begin = try await vault.call(path: "register/begin", payload: [
            "user_handle": cfg.userHandle,
            "user_name": (cfg.userName?.isEmpty == false) ? cfg.userName! : cfg.userHandle
        ], vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath)

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
        ], vaultAddr: cfg.vaultAddr, mountPath: cfg.mountPath)

        return ["begin": begin, "finish": finish]
    }

    static func runLogin(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        let vault = VaultClient()
        log("[passkey] login: calling Vault login/begin ...")
        let begin = try await vault.call(path: "login/begin", payload: [
            "role": cfg.role ?? "default",
            "user_handle": cfg.userHandle
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
  const pad = '='.repeat((4 - (b64url.length % 4)) % 4);
  const b64 = (b64url + pad).replace(/-/g,'+').replace(/_/g,'/');
  const str = atob(b64);
  const bytes = new Uint8Array(str.length);
  for (let i=0;i<str.length;i++) bytes[i] = str.charCodeAt(i);
  return bytes.buffer;
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
  if (pk.user && typeof pk.user.id === 'string') pk.user.id = b64urlToBuf(pk.user.id);
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
      <label>User handle (sub)</label>
      <input id="userHandle" placeholder="e.g. employeeId or username" />
    </div>
    <div class="col">
      <label>User name (optional, for display)</label>
      <input id="userName" placeholder="optional" />
    </div>
  </div>

  <div class="row">
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
      const pad = '='.repeat((4 - (b64url.length % 4)) % 4);
      const b64 = (b64url + pad).replace(/-/g, '+').replace(/_/g, '/');
      const str = atob(b64);
      const bytes = new Uint8Array(str.length);
      for (let i = 0; i < str.length; i++) bytes[i] = str.charCodeAt(i);
      return bytes.buffer;
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
      if (pk.user && typeof pk.user.id === 'string') {
        pk.user.id = base64urlToBuf(pk.user.id);
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

    async function register() {
      try {
        const vaultAddr = document.getElementById('vaultAddr').value;
        const mountPath = document.getElementById('mountPath').value;
        const userHandle = document.getElementById('userHandle').value;
        const userName = document.getElementById('userName').value;

        const result = await callNative('register', { vaultAddr, mountPath, userHandle, userName });
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
        const role = document.getElementById('role').value;

        const result = await callNative('login', { vaultAddr, mountPath, role, userHandle });
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

