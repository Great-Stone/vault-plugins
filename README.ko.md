# vault-plugins

HashiCorp Vault용 커스텀 플러그인 모음입니다. 이 저장소의 플러그인들은 Go로 작성되며 Vault SDK의 `framework.Backend` 패턴과 플러그인 multiplexing(`plugin.ServeMultiplex`)을 사용합니다.

영문 문서(English): [`README.md`](./README.md)

## 포함된 플러그인

| 디렉터리 | 설명 |
|---|---|
| `plugins/vault-plugin-secrets-kafka` | Kafka 인증용 시크릿 엔진(동적 SCRAM 발급/해지 + 정적 번들 확장 기반). 로컬 `docker compose` 예제와 Spring Boot 검증 UI 포함 |
| `plugins/vault-plugin-secrets-machine` | 머신(로컬 OS 계정) 정적 자격증명: 비밀번호를 보관하고 SSH(Linux)·WinRM(Windows)으로 주기적 로테이션. 마운트 예: `machine/`; Database 정적 역할과 유사한 모델. |
| `plugins/vault-plugin-secrets-github` | GitHub App 설치 액세스 토큰(installation access token) 발급용 시크릿 엔진 |
| `plugins/vault-plugin-auth-mac-passkey` | macOS Touch ID 기반 Passkey(WebAuthn)로 Vault 토큰을 발급하는 Auth method 플러그인(로컬 macOS 헬퍼와 함께 사용) |

## CI 빌드 상태

교차 컴파일 타깃: **linux** (`amd64`, `arm`, `arm64`), **windows** (`amd64`, `arm64`), **darwin** (`arm64`). 각 셀은 해당 워크플로로 연결되며 `main` 브랜치 기준 최근 실행 결과를 보여줍니다. 배지의 대체 텍스트(alt)에는 **플랫폼 이름만** 적었습니다(플러그인은 첫 번째 열에서 구분).

**vault-plugin-auth-mac-passkey**의 Go 플러그인은 시크리트 플러그인과 같이 **플랫폼별 워크플로**([`scripts/generate-plugin-ci-workflows.sh`](scripts/generate-plugin-ci-workflows.sh)로 생성)를 사용합니다. Swift CLI와 macOS `.app`은 **별도 워크플로**로 빌드 상태를 각각 확인할 수 있습니다(아래 표).

배지 URL 기준 저장소: [`Great-Stone/vault-plugins`](https://github.com/Great-Stone/vault-plugins). 포크한 경우 이미지 URL의 owner/repo를 맞추거나, 워크플로를 바꾼 뒤 [`scripts/generate-plugin-ci-workflows.sh`](scripts/generate-plugin-ci-workflows.sh)로 호출부를 재생성할 수 있습니다.

| 플러그인 | linux/amd64 | linux/arm | linux/arm64 | windows/amd64 | windows/arm64 | darwin/arm64 |
|----------|-------------|-----------|---------------|---------------|----------------|--------------|
| [secret-kafka](plugins/vault-plugin-secrets-kafka) | [![linux/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-amd64.yml) | [![linux/arm](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-arm.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-arm.yml) | [![linux/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-linux-arm64.yml) | [![windows/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-windows-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-windows-amd64.yml) | [![windows/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-windows-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-windows-arm64.yml) | [![darwin/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-darwin-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-kafka-darwin-arm64.yml) |
| [secret-machine](plugins/vault-plugin-secrets-machine) | [![linux/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-amd64.yml) | [![linux/arm](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-arm.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-arm.yml) | [![linux/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-linux-arm64.yml) | [![windows/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-windows-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-windows-amd64.yml) | [![windows/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-windows-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-windows-arm64.yml) | [![darwin/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-darwin-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-machine-darwin-arm64.yml) |
| [secret-github](plugins/vault-plugin-secrets-github) | [![linux/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-amd64.yml) | [![linux/arm](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-arm.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-arm.yml) | [![linux/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-linux-arm64.yml) | [![windows/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-windows-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-windows-amd64.yml) | [![windows/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-windows-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-windows-arm64.yml) | [![darwin/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-darwin-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-github-darwin-arm64.yml) |
| [auth-mac-passkey](plugins/vault-plugin-auth-mac-passkey) | [![linux/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-amd64.yml) | [![linux/arm](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-arm.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-arm.yml) | [![linux/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-linux-arm64.yml) | [![windows/amd64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-windows-amd64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-windows-amd64.yml) | [![windows/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-windows-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-windows-arm64.yml) | [![darwin/arm64](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-darwin-arm64.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-darwin-arm64.yml) |

#### auth-mac-passkey: 로컬 헬퍼(별도 워크플로)

| 구성 요소 | 상태 |
|-----------|------|
| Swift CLI (`localhelper/vault-login-passkey-macos`) | [![Swift CLI](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-swift-cli.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-swift-cli.yml) |
| `.app` 번들 (`localhelper/vault-login-passkey-macos-app`) | [![macOS app](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-macos-app.yml/badge.svg?branch=main)](https://github.com/Great-Stone/vault-plugins/actions/workflows/plugin-auth-mac-passkey-macos-app.yml) |


## 릴리스

모든 플러그인 바이너리를 포함하는 GitHub Release를 발행하려면 GitHub Actions의 [`Release plugins`](https://github.com/Great-Stone/vault-plugins/actions/workflows/release-plugins.yml) 워크플로를 수동 실행하고, `v1.2.3` 같은 semver 버전을 입력합니다. 워크플로는 지원 타깃으로 교차 컴파일한 zip과 `SHA256SUMS`를 생성하고, 현재 `main` 커밋에 태그를 찍은 뒤 Release 자산으로 업로드합니다.

## 참고

- Vault Plugin development: `https://developer.hashicorp.com/vault/docs/plugins/plugin-development`
- Register a plugin: `https://developer.hashicorp.com/vault/docs/plugins/register-plugin`

