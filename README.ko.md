# vault-plugins

HashiCorp Vault용 커스텀 플러그인 모음입니다. 이 저장소의 플러그인들은 Go로 작성되며 Vault SDK의 `framework.Backend` 패턴과 플러그인 multiplexing(`plugin.ServeMultiplex`)을 사용합니다.

## 포함된 플러그인

| 디렉터리 | 설명 |
|---|---|
| `plugins/vault-plugin-secrets-kafka` | Kafka 인증용 시크릿 엔진(동적 SCRAM 발급/해지 + 정적 번들 확장 기반). 로컬 `docker compose` 예제와 Spring Boot 검증 UI 포함 |
| `plugins/vault-plugin-secrets-github` | GitHub App 설치 액세스 토큰(installation access token) 발급용 시크릿 엔진 |

브라우저에서 `http://localhost:8080`으로 접속하면 1초 주기 검증/히스토리/수동 renew·revoke 등을 확인할 수 있습니다.

## 참고

- Vault Plugin development: `https://developer.hashicorp.com/vault/docs/plugins/plugin-development`
- Register a plugin: `https://developer.hashicorp.com/vault/docs/plugins/register-plugin`

