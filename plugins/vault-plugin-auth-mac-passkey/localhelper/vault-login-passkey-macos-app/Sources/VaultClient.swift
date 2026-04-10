import Foundation

struct VaultLoginConfig {
    var vaultAddr: String
    var mountPath: String
    var userHandle: String
    var userName: String?
    var role: String?
    /// Used for `register/*` when non-empty; otherwise env / ~/.vault-token.
    var vaultRegisterToken: String?
}

enum VaultLoginPasskeyError: Error, CustomStringConvertible, LocalizedError {
    case invalidVaultAddr
    case missing(String)
    /// `auth/token/lookup-self` returned 401/403 (common for passkey-issued tokens).
    case lookupSelfPermissionDenied

    var description: String {
        switch self {
        case .invalidVaultAddr: return "invalid vaultAddr"
        case .missing(let s): return "missing: \(s)"
        case .lookupSelfPermissionDenied:
            return """
            Vault denied auth/token/lookup-self for this token (HTTP 401/403). Passkey-issued tokens usually cannot call that path. \
            Enter user_handle (entity name or id from registration), or use a bootstrap token whose policy allows path \"auth/token/lookup-self\". \
            Login uses only the Vault token field (not VAULT_TOKEN or ~/.vault-token) to auto-fill user_handle — leave the token field empty and set user_handle to log in with a passkey token on disk.
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

        let url = baseURL
            .appendingPathComponent("v1")
            .appendingPathComponent(mount)
            .appendingPathComponent(path)

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
            // NSError.code is not always the HTTP status after bridging; prefer userInfo["httpStatus"].
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
            throw VaultLoginPasskeyError.missing("token has no entity_id")
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
        let addr = vaultAddr.trimmingCharacters(in: .whitespacesAndNewlines)
        var path = v1Path.trimmingCharacters(in: .whitespacesAndNewlines)
        path = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))

        let base = addr.trimmingCharacters(in: .whitespacesAndNewlines)
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

extension Data {
    func base64URLEncodedString() -> String {
        self.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}

