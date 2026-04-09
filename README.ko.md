# vault-plugins

HashiCorp Vault용 커스텀 플러그인 모음입니다. 이 저장소의 플러그인들은 Go로 작성되며 Vault SDK의 `framework.Backend` 패턴과 플러그인 multiplexing(`plugin.ServeMultiplex`)을 사용합니다.

## 포함된 플러그인

| 디렉터리 | 설명 |
|---|---|
| `plugins/vault-plugin-secrets-kafka` | Kafka 인증용 시크릿 엔진(동적 SCRAM 발급/해지 + 정적 번들 확장 기반). 로컬 `docker compose` 예제와 Spring Boot 검증 UI 포함 |
| `plugins/vault-plugin-secrets-machine` | 머신(로컬 OS 계정) 정적 자격증명: 비밀번호를 보관하고 SSH(Linux)·WinRM(Windows)으로 주기적 로테이션. 마운트 예: `machine/`; Database 정적 역할과 유사한 모델. |
| `plugins/vault-plugin-secrets-github` | GitHub App 설치 액세스 토큰(installation access token) 발급용 시크릿 엔진 |

## 참고

- Vault Plugin development: `https://developer.hashicorp.com/vault/docs/plugins/plugin-development`
- Register a plugin: `https://developer.hashicorp.com/vault/docs/plugins/register-plugin`

