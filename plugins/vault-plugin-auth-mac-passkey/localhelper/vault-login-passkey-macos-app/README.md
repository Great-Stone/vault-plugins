# VaultLoginPasskey.app (macOS)

이 디렉토리는 `vault-plugin-auth-mac-passkey`와 함께 쓰는 **macOS .app** 클라이언트입니다.

이 .app은 **설정 UI**를 제공하고, Passkey(WebAuthn) “승인”은 **Safari를 통해 수행**합니다(= WebAuthn `navigator.credentials.*`).

> 참고: “브라우저 없이” macOS 네이티브(AuthenticationServices) Passkey로도 구현할 수 있지만,\n+> 코드사인/엔타이틀먼트/Associated Domains 등 설정이 필요하고 `localhost` 개발 환경에서는 제약이 커서,\n+> 현재는 Safari(WebAuthn) 경로를 기본으로 둡니다.

## Build

Xcode 없이도 로컬에서 실행 가능한 `.app` 번들을 생성합니다.

```bash
cd plugins/vault-plugin-auth-mac-passkey/localhelper/vault-login-passkey-macos-app
chmod +x build-app.sh
./build-app.sh
```

결과물:

```bash
./dist/VaultLoginPasskey.app
```

## Run

```bash
open "./dist/VaultLoginPasskey.app"
```

앱에서:

- Vault address: 예) `http://localhost:8200`
- Mount path: 예) `auth/passkey`
- User handle: 예) `gs.lee`
- Role: 예) `default`

그리고 `Register` → `Login` 순서로 실행합니다.

## 개발(localhost) 관련

이 앱은 `http://localhost:8765/`를 사용해 Safari WebAuthn을 수행합니다. Vault 설정에서 아래가 일치해야 합니다.

- `rp_id = \"localhost\"`
- `allowed_origins = \"http://localhost:8765\"`

