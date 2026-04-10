import SwiftUI

/// Same `UserDefaults` payload as `vault-login-passkey-macos` (WK `ui`) so both helpers share form state.
private enum PasskeyUIFormSync {
    static let defaultsKey = "vaultLoginPasskey.uiForm.v1"

    struct State: Codable {
        var vaultAddr: String
        var mountPath: String
        var userName: String
        var userHandle: String
        var role: String
    }

    static func load() -> State? {
        guard let data = UserDefaults.standard.data(forKey: defaultsKey) else { return nil }
        return try? JSONDecoder().decode(State.self, from: data)
    }

    static func save(vaultAddr: String, mountPath: String, userName: String, userHandle: String, role: String) {
        let roleTrim = role.trimmingCharacters(in: .whitespacesAndNewlines)
        let s = State(
            vaultAddr: vaultAddr.trimmingCharacters(in: .whitespacesAndNewlines),
            mountPath: mountPath.trimmingCharacters(in: .whitespacesAndNewlines),
            userName: userName.trimmingCharacters(in: .whitespacesAndNewlines),
            userHandle: userHandle.trimmingCharacters(in: .whitespacesAndNewlines),
            role: roleTrim.isEmpty ? "default" : roleTrim
        )
        guard let data = try? JSONEncoder().encode(s) else { return }
        UserDefaults.standard.set(data, forKey: defaultsKey)
    }
}

struct ContentView: View {
    /// Match WK HTML: empty until user types or shared form loads (localhost often used in dev).
    @AppStorage("vaultAddr") private var vaultAddr: String = ""
    @AppStorage("mountPath") private var mountPath: String = "auth/passkey"
    @AppStorage("userHandle") private var userHandle: String = ""
    @AppStorage("userName") private var userName: String = ""
    @AppStorage("vaultRegisterToken") private var vaultRegisterToken: String = ""
    @AppStorage("role") private var role: String = "default"

    @State private var busy: Bool = false
    @State private var output: String = ""
    @State private var didApplySharedFormFromWKUI: Bool = false

    private let vault = VaultClient()

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Vault Passkey (Touch ID) Login")
                .font(.title2)
                .bold()

            // Two-column rows — same order and copy as `VaultLoginPasskeyApp.html` in vault-login-passkey-macos.
            formRow(
                label: "Vault address",
                content: {
                    TextField("https://vault.example.com", text: $vaultAddr)
                        .textFieldStyle(.roundedBorder)
                },
                label2: "Mount path",
                content2: {
                    TextField("auth/passkey", text: $mountPath)
                        .textFieldStyle(.roundedBorder)
                }
            )
            formRow(
                label: "Vault token",
                content: {
                    SecureField("register: required. login: optional (field only — lookup-self; clear if passkey token)", text: $vaultRegisterToken)
                        .textFieldStyle(.roundedBorder)
                },
                label2: "User name (optional, WebAuthn display; register)",
                content2: {
                    TextField("defaults to entity name / id", text: $userName)
                        .textFieldStyle(.roundedBorder)
                }
            )
            formRow(
                label: "user_handle (login; filled automatically after Register)",
                content: {
                    TextField("entity name or id — saved when registration succeeds", text: $userHandle)
                        .textFieldStyle(.roundedBorder)
                },
                label2: "Role (for login)",
                content2: {
                    TextField("default", text: $role)
                        .textFieldStyle(.roundedBorder)
                }
            )

            HStack(spacing: 10) {
                Button("Register passkey") { run(.register) }
                    .disabled(busy)
                Button("Login (writes ~/.vault-token)") { run(.login) }
                    .disabled(busy)
                Spacer()
                if busy {
                    ProgressView()
                }
            }

            Text("Output")
                .font(.headline)
            ScrollView {
                Text(output.isEmpty ? "Ready." : output)
                    .font(.body)
                    .monospaced()
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(minHeight: 240, alignment: .topLeading)
                    .background(Color(nsColor: .textBackgroundColor))
                    .overlay(
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(Color.gray.opacity(0.3))
                    )
            }
        }
        .padding(16)
        .frame(minWidth: 760, minHeight: 520)
        .onAppear {
            guard !didApplySharedFormFromWKUI else { return }
            if let s = PasskeyUIFormSync.load() {
                vaultAddr = s.vaultAddr
                mountPath = s.mountPath
                userName = s.userName
                userHandle = s.userHandle
                role = s.role
            }
            didApplySharedFormFromWKUI = true
        }
    }

    @ViewBuilder
    private func formRow<L: View, R: View>(
        label: String,
        @ViewBuilder content: () -> L,
        label2: String,
        @ViewBuilder content2: () -> R
    ) -> some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(label)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                content()
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .leading, spacing: 4) {
                Text(label2)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                content2()
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    enum Action { case register, login }

    private func run(_ action: Action) {
        let cfg = VaultLoginConfig(
            vaultAddr: vaultAddr,
            mountPath: mountPath,
            userHandle: userHandle.trimmingCharacters(in: .whitespacesAndNewlines),
            userName: userName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : userName,
            role: role.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "default" : role,
            vaultRegisterToken: vaultRegisterToken.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : vaultRegisterToken
        )

        busy = true
        output = "Starting \(action == .register ? "register" : "login")..."

        Task {
            do {
                let result: [String: Any]
                switch action {
                case .register:
                    result = try await register(cfg)
                    await MainActor.run {
                        guard let sf = result["savedForm"] as? [String: Any] else { return }
                        if let s = sf["vaultAddr"] as? String { vaultAddr = s }
                        if let s = sf["mountPath"] as? String { mountPath = s }
                        if let s = sf["userName"] as? String { userName = s }
                        if let s = sf["userHandle"] as? String { userHandle = s }
                        if let s = sf["role"] as? String { role = s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "default" : s }
                        PasskeyUIFormSync.save(
                            vaultAddr: vaultAddr,
                            mountPath: mountPath,
                            userName: userName,
                            userHandle: userHandle,
                            role: role
                        )
                    }
                case .login:
                    result = try await login(cfg)
                }
                let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])
                await MainActor.run {
                    output = String(data: data, encoding: .utf8) ?? "\(result)"
                    busy = false
                    if case .login = action {
                        PasskeyUIFormSync.save(
                            vaultAddr: vaultAddr,
                            mountPath: mountPath,
                            userName: userName,
                            userHandle: userHandle,
                            role: role
                        )
                    }
                }
            } catch {
                await MainActor.run {
                    output = (error as? LocalizedError)?.errorDescription ?? String(describing: error)
                    busy = false
                }
            }
        }
    }

    private func register(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        guard !cfg.vaultAddr.isEmpty else { throw VaultLoginPasskeyError.missing("vaultAddr") }
        guard let token = VaultTokenStore.resolveVaultToken(explicit: cfg.vaultRegisterToken), !token.isEmpty else {
            throw VaultLoginPasskeyError.missing("Vault token for registration (field above, VAULT_TOKEN, or ~/.vault-token)")
        }

        var beginPayload: [String: Any] = [:]
        if let un = cfg.userName, !un.isEmpty {
            beginPayload["user_name"] = un
        }

        let begin = try await vault.call(
            path: "register/begin",
            payload: beginPayload,
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath,
            vaultToken: token
        )

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 10, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .register, options: optionsAny)
        let credentialB64 = try JSONSerialization.data(withJSONObject: webauthnJSON, options: []).base64URLEncodedString()

        let finish = try await vault.call(
            path: "register/finish",
            payload: [
                "session_id": sessionId,
                "credential": credentialB64
            ],
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath,
            vaultToken: token
        )

        var out: [String: Any] = ["begin": begin, "finish": finish]
        let userHandleField = cfg.userHandle.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedHandle = Self.registerFinishUserHandle(finish) ?? (userHandleField.isEmpty ? nil : userHandleField)
        if let uh = resolvedHandle {
            out["savedUserHandle"] = uh
        }
        let roleSaved = (cfg.role ?? "default").trimmingCharacters(in: .whitespacesAndNewlines)
        let roleFinal = roleSaved.isEmpty ? "default" : roleSaved
        let nameSaved = cfg.userName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let handleStored = resolvedHandle ?? userHandleField
        out["savedForm"] = [
            "vaultAddr": cfg.vaultAddr,
            "mountPath": cfg.mountPath,
            "userName": nameSaved,
            "userHandle": handleStored,
            "role": roleFinal
        ]
        return out
    }

    /// Vault `register/finish` JSON may nest `user_handle` arbitrarily; walk the tree.
    private static func registerFinishUserHandle(_ finish: [String: Any]) -> String? {
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

    private func login(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        guard !cfg.vaultAddr.isEmpty else { throw VaultLoginPasskeyError.missing("vaultAddr") }

        var userHandle = cfg.userHandle.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedLoginToken = cfg.vaultRegisterToken?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if userHandle.isEmpty, !trimmedLoginToken.isEmpty {
            let r = try await vault.resolvePasskeyPrincipal(vaultAddr: cfg.vaultAddr, vaultToken: trimmedLoginToken)
            userHandle = r.userHandle
        }
        guard !userHandle.isEmpty else {
            throw VaultLoginPasskeyError.missing(
                "user_handle: enter entity name or id, or paste a Vault token in the field above that may call auth/token/lookup-self (VAULT_TOKEN and ~/.vault-token are not used for login auto-resolve)"
            )
        }

        let begin = try await vault.call(
            path: "login/begin",
            payload: [
                "role": cfg.role ?? "default",
                "user_handle": userHandle
            ],
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath
        )

        let sessionId = (begin["session_id"] as? String)
            ?? ((begin["data"] as? [String: Any])?["session_id"] as? String)
        let optionsAny = (begin["options"] as? [String: Any])
            ?? ((begin["data"] as? [String: Any])?["options"] as? [String: Any])
        guard let sessionId, let optionsAny else {
            throw NSError(domain: "vault-login-passkey", code: 20, userInfo: [NSLocalizedDescriptionKey: "missing session/options", "begin": begin])
        }

        let webauthnJSON = try await SafariWebAuthnFlow.run(mode: .login, options: optionsAny)
        let credentialB64 = try JSONSerialization.data(withJSONObject: webauthnJSON, options: []).base64URLEncodedString()

        let finish = try await vault.call(
            path: "login/finish",
            payload: [
                "session_id": sessionId,
                "credential": credentialB64
            ],
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath
        )

        if let auth = finish["auth"] as? [String: Any],
           let token = auth["client_token"] as? String,
           !token.isEmpty {
            try VaultTokenStore.writeVaultToken(token)
            return ["begin": begin, "finish": finish, "savedToken": true]
        }
        return ["begin": begin, "finish": finish, "savedToken": false]
    }
}

