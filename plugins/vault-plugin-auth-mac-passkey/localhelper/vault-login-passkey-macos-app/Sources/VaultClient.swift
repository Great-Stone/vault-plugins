import Foundation

struct VaultLoginConfig {
    var vaultAddr: String
    var mountPath: String
    var userHandle: String
    var userName: String?
    var role: String?
}

enum VaultLoginPasskeyError: Error, CustomStringConvertible {
    case invalidVaultAddr
    case missing(String)

    var description: String {
        switch self {
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

        let url = baseURL
            .appendingPathComponent("v1")
            .appendingPathComponent(mount)
            .appendingPathComponent(path)

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
}

extension Data {
    func base64URLEncodedString() -> String {
        self.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}

