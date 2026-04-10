# vault-login-passkey (macOS)

Native macOS helper that opens a small window (WKWebView) to run the WebAuthn ceremony with Touch ID and call Vault's `passkey` auth plugin endpoints.

This helper is intended to be used with:

- `plugins/vault-plugin-auth-mac-passkey`

## Build

```bash
cd localhelper/vault-login-passkey-macos
swift build -c release
```

Binary will be at `.build/release/vault-login-passkey`.

## Run

```bash
.build/release/vault-login-passkey ui
```

Then fill in:

- `Vault address`: e.g. `https://vault.example.com`
- `Mount path`: default `auth/passkey`
- `User handle`: your `sub` (recommended: employeeId or username, not email)
- `Role`: Vault role for token policy/TTL

If login succeeds, it writes the Vault token to `~/.vault-token`.

## CLI mode

You can also run the helper without the UI (it will still open Safari for the Passkey ceremony).

```bash
# Register
.build/release/vault-login-passkey cli register \
  --vault-addr http://localhost:8200 \
  --mount-path auth/passkey \
  --user-handle gs.lee

# Login (writes ~/.vault-token)
.build/release/vault-login-passkey cli login \
  --vault-addr http://localhost:8200 \
  --mount-path auth/passkey \
  --user-handle gs.lee \
  --role default
```

## Notes

- This helper opens **Safari** to perform WebAuthn (`navigator.credentials.*`) because `AuthenticationServices` requires a signed `.app` bundle with an application identifier (SwiftPM-built CLI binaries fail with `AuthorizationError Code=1004`).
- Passkeys are bound to a **Relying Party ID (RP ID)**. Ensure your Vault plugin `rp_id` matches the RP ID used by the browser flow (typically `localhost` for dev, or your real domain in prod).
- For dev, the helper prefers opening `http://localhost:8765/` so Vault `allowed_origins` can be configured to a stable origin.

## Native (.app) mode (AuthenticationServices)

If you need a browser-less native Passkey flow, see:

- `localhelper/vault-login-passkey-macos-app/`

