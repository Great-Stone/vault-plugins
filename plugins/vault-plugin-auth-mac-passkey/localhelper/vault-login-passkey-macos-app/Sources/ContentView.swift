import SwiftUI

struct ContentView: View {
    @AppStorage("vaultAddr") private var vaultAddr: String = "http://localhost:8200"
    @AppStorage("mountPath") private var mountPath: String = "auth/passkey"
    @AppStorage("userHandle") private var userHandle: String = ""
    @AppStorage("userName") private var userName: String = ""
    @AppStorage("role") private var role: String = "default"

    @State private var busy: Bool = false
    @State private var output: String = ""

    private let vault = VaultClient()

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Vault Passkey Login (.app)")
                .font(.title2)
                .bold()

            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
                GridRow {
                    Text("Vault address").frame(width: 120, alignment: .leading)
                    TextField("http://localhost:8200", text: $vaultAddr).textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("Mount path").frame(width: 120, alignment: .leading)
                    TextField("auth/passkey", text: $mountPath).textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("User handle").frame(width: 120, alignment: .leading)
                    TextField("gs.lee", text: $userHandle).textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("User name").frame(width: 120, alignment: .leading)
                    TextField("(optional)", text: $userName).textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("Role").frame(width: 120, alignment: .leading)
                    TextField("default", text: $role).textFieldStyle(.roundedBorder)
                }
            }

            HStack(spacing: 10) {
                Button("Register") { run(.register) }
                    .disabled(busy)
                Button("Login (write ~/.vault-token)") { run(.login) }
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
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
                    .padding(10)
                    .background(Color(nsColor: .textBackgroundColor))
                    .overlay(
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(Color.gray.opacity(0.3))
                    )
            }
        }
        .padding(16)
        .frame(minWidth: 760, minHeight: 520)
    }

    enum Action { case register, login }

    private func run(_ action: Action) {
        let cfg = VaultLoginConfig(
            vaultAddr: vaultAddr,
            mountPath: mountPath,
            userHandle: userHandle.trimmingCharacters(in: .whitespacesAndNewlines),
            userName: userName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : userName,
            role: role.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "default" : role
        )

        busy = true
        output = "Starting \(action == .register ? "register" : "login")..."

        Task {
            do {
                let result: [String: Any]
                switch action {
                case .register:
                    result = try await register(cfg)
                case .login:
                    result = try await login(cfg)
                }
                let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])
                await MainActor.run {
                    output = String(data: data, encoding: .utf8) ?? "\(result)"
                    busy = false
                }
            } catch {
                await MainActor.run {
                    output = String(describing: error)
                    busy = false
                }
            }
        }
    }

    private func register(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        guard !cfg.vaultAddr.isEmpty else { throw VaultLoginPasskeyError.missing("vaultAddr") }
        guard !cfg.userHandle.isEmpty else { throw VaultLoginPasskeyError.missing("userHandle") }

        let begin = try await vault.call(
            path: "register/begin",
            payload: [
                "user_handle": cfg.userHandle,
                "user_name": (cfg.userName?.isEmpty == false) ? cfg.userName! : cfg.userHandle
            ],
            vaultAddr: cfg.vaultAddr,
            mountPath: cfg.mountPath
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
            mountPath: cfg.mountPath
        )

        return ["begin": begin, "finish": finish]
    }

    private func login(_ cfg: VaultLoginConfig) async throws -> [String: Any] {
        guard !cfg.vaultAddr.isEmpty else { throw VaultLoginPasskeyError.missing("vaultAddr") }
        guard !cfg.userHandle.isEmpty else { throw VaultLoginPasskeyError.missing("userHandle") }

        let begin = try await vault.call(
            path: "login/begin",
            payload: [
                "role": cfg.role ?? "default",
                "user_handle": cfg.userHandle
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

