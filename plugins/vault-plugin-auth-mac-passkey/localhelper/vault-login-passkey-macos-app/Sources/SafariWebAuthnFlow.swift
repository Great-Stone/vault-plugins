import AppKit
import Foundation
import Network

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
    private var connectionHandlers: [ObjectIdentifier: HTTPConnectionHandler] = [:]

    init(mode: SafariWebAuthnMode, options: [String: Any]) {
        self.mode = mode
        self.options = options
    }

    func run() async throws -> [String: Any] {
        let port = try await startListener()
        let url = URL(string: "http://localhost:\(port)/")!
        NSWorkspace.shared.open(url)
        return try await withCheckedThrowingContinuation { cont in
            self.resultContinuation = cont
        }
    }

    private func startListener() async throws -> UInt16 {
        let params = NWParameters.tcp
        params.allowLocalEndpointReuse = true

        let preferredPort: UInt16 = 8765
        if let p = NWEndpoint.Port(rawValue: preferredPort),
           let stable = try? NWListener(using: params, on: p) {
            let listener = stable
            self.listener = listener
            listener.newConnectionHandler = { [weak self] conn in
                self?.handle(conn)
            }
            return try await withCheckedThrowingContinuation { cont in
                listener.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
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

        let listener = try NWListener(using: params, on: .any)
        self.listener = listener
        listener.newConnectionHandler = { [weak self] conn in
            self?.handle(conn)
        }
        return try await withCheckedThrowingContinuation { cont in
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    if let port = listener.port?.rawValue {
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
        connectionHandlers[ObjectIdentifier(handler)] = handler
        handler.start()
    }

    fileprivate func dropHandler(_ handler: HTTPConnectionHandler) {
        connectionHandlers.removeValue(forKey: ObjectIdentifier(handler))
    }

    fileprivate func respond(_ conn: NWConnection, status: String, contentType: String, body: Data) {
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
  return { name: e.name, message: e.message, stack: e.stack };
}
function credentialToJSON(cred) {
  try {
    if (cred && typeof cred.toJSON === 'function') return cred.toJSON();
  } catch (e) {
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

        var contentLength = 0
        for line in lines.dropFirst() {
            let lower = line.lowercased()
            if lower.hasPrefix("content-length:") {
                let v = line.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: true).last.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
                contentLength = Int(v) ?? 0
            }
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
            let html = server.pageHTML()
            server.respond(conn, status: "200 OK", contentType: "text/html; charset=utf-8", body: Data(html.utf8))
            return true
        }

        if requestLine.hasPrefix("GET /favicon.ico ") || requestLine.hasPrefix("GET /favicon.ico HTTP/") ||
            requestLine.hasPrefix("GET /apple-touch-icon") {
            server.respond(conn, status: "204 No Content", contentType: "text/plain; charset=utf-8", body: Data())
            return true
        }

        if requestLine.hasPrefix("POST /result ") || requestLine.hasPrefix("POST /result HTTP/") {
            if let obj = try? JSONSerialization.jsonObject(with: body, options: []),
               let dict = obj as? [String: Any] {
                server.respond(conn, status: "200 OK", contentType: "application/json", body: Data("{\"ok\":true}".utf8))
                server.finish(dict)
                return true
            }
            server.respond(conn, status: "400 Bad Request", contentType: "text/plain; charset=utf-8", body: Data("bad json".utf8))
            return true
        }

        server.respond(conn, status: "404 Not Found", contentType: "text/plain; charset=utf-8", body: Data("not found".utf8))
        return true
    }
}

