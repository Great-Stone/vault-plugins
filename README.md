# vault-plugins

HashiCorp Vault용 커스텀 시크릿 엔진 플러그인 모음입니다. 각 플러그인은 Go로 작성되며 [Vault 플러그인 개발 가이드](https://developer.hashicorp.com/vault/docs/plugins/plugin-development)의 `plugin.ServeMultiplex` 및 `github.com/hashicorp/vault/sdk`의 `framework.Backend` 패턴을 따릅니다.

## 포함된 플러그인

| 디렉터리 | 설명 |
|----------|------|
| [plugins/vault-plugin-secrets-github](plugins/vault-plugin-secrets-github/) | GitHub App **설치 액세스 토큰**을 발급합니다. 조직/사용자 fine-grained PAT의 **신규 생성**은 GitHub REST API에서 지원하지 않으므로, 동적 발급은 GitHub App 흐름으로 제공합니다. |

## 참고 자료

- [Plugin development](https://developer.hashicorp.com/vault/docs/plugins/plugin-development)
- [Register a plugin](https://developer.hashicorp.com/vault/docs/plugins/register-plugin)
- 예시 레포지토리: [vault-plugin-secrets-openai](https://github.com/gitrgoliveira/vault-plugin-secrets-openai)
