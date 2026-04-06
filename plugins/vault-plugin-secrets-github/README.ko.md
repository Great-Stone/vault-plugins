# vault-plugin-secrets-github

영문 문서(English): [`README.md`](./README.md)

Vault **시크릿 엔진 플러그인**으로, GitHub App 자격 증명(App ID + PEM 개인 키)을 사용해 **GitHub App 설치 액세스 토큰(installation access token)** 을 발급합니다.

## 제약: fine-grained PAT와 REST API

GitHub는 **fine-grained personal access token 문자열을 새로 생성하는 REST 엔드포인트**를 제공하지 않습니다. `POST /orgs/{org}/personal-access-tokens` 같은 조직 단위 API는 “기존 토큰의 관리/취소” 용도이며, 토큰 시크릿 문자열을 새로 발급하지 않습니다.

따라서 자동화·동적 발급이 필요하면 GitHub가 권장하는 **GitHub App + 설치 액세스 토큰** 흐름을 쓰는 것이 맞고, 본 플러그인은 그 흐름을 Vault Secret Engine으로 제공합니다. PAT가 반드시 필요하면 GitHub UI에서 생성한 뒤 Vault(KV 등)에 정적 시크릿으로 저장하는 방식을 검토하세요.

## 요구 사항

- Go 1.23+
- Vault 1.15+ (플러그인 multiplexing, SDK `v0.5.4+` 호환)

## 빌드

```bash
cd plugins/vault-plugin-secrets-github
make build
# 산출물: bin/vault-plugin-secrets-github
```

또는:

```bash
go build -o bin/vault-plugin-secrets-github ./cmd/vault-plugin-secrets-github
```

## 등록 및 활성화(예시)

Vault를 dev 모드로 실행할 때 `-dev-plugin-dir`로 플러그인 바이너리가 있는 디렉터리를 지정합니다.

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./bin
```

SHA256을 계산해 카탈로그에 등록 후 시크릿 엔진을 활성화합니다.

```bash
PLUGIN=$(realpath ./bin/vault-plugin-secrets-github)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
VAULT_TOKEN=root
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-github
vault secrets enable -path=github -plugin-name=vault-plugin-secrets-github plugin
```

## 설정

### `github/config`

| 필드 | 설명 |
|---|---|
| `app_id` | GitHub App ID (필수) |
| `private_key` | App의 PEM RSA 개인 키 (필수) |
| `base_url` | GitHub Enterprise Server 사용 시 API base (예: `https://github.example.com/api/v3`). 생략 시 `https://api.github.com` |

#### GitHub 준비(UI·문서)

`app_id`와 `private_key`는 **GitHub에서 GitHub App을 등록하고 개인 키를 발급**할 때 얻습니다. Vault 전용 UI는 없으며, 아래 GitHub 문서를 따르면 됩니다.

- GitHub App 개요: `https://docs.github.com/en/apps`
- GitHub App 등록: `https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app`
- GitHub App private key 관리: `https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps`
- GitHub App 설치: `https://docs.github.com/en/apps/creating-github-apps/installing-github-apps/installing-your-own-github-app`

로컬 Vault를 사용해도 GitHub App 등록에는 문제가 없습니다. App은 GitHub.com/GHES에서 생성되고, Vault는 GitHub REST API로 **아웃바운드 호출**만 수행합니다.

#### 예시: config 작성

```bash
vault write github/config \
  app_id=123456 \
  private_key=@/path/to/app-private-key.pem \
  base_url="https://api.github.com"
```

### 역할: `github/roles/<name>`

역할에는 최소 `installation_id`가 필요합니다(앱을 설치한 뒤 확인).

#### `installation_id` 확인 방법

- GitHub.com → **Settings**(개인) 또는 **조직 Settings** → **Developer settings** → **GitHub Apps** → 해당 앱 → **Install App** / **Configure**
- URL에 `.../installations/<숫자>` 형태가 자주 보이며, 이 숫자가 `installation_id`입니다.

| 필드 | 설명 |
|---|---|
| `installation_id` | 설치 ID(필수). GitHub가 설치 시 부여하는 정수이며 임의로 만들 수 없습니다. |
| `repositories` | 선택. 저장소 이름 목록(쉼표 구분). `owner/repo` 형태로 적어도 됩니다. |
| `repository_ids` | 선택. 저장소 숫자 ID 목록(쉼표 구분). |
| `permissions` | 선택. 설치 토큰에 부여할 권한 맵(키/값은 GitHub REST 스펙). |
| `ttl` / `max_ttl` | 기본 TTL 및 상한(초). |

#### 예시: role 구성

```bash
vault write github/roles/ci \
  installation_id=87654321 \
  repositories="my-org/my-repo-a,my-org/my-repo-b" \
  permissions=contents=write \
  ttl=3600 \
  max_ttl=7200
```

#### `permissions` 여러 개 넣기(Vault CLI)

```bash
vault write github/roles/ci \
  installation_id=12345678 \
  ttl=3600 \
  max_ttl=7200 \
  permissions=contents=read \
  permissions=issues=write \
  permissions=pull_requests=write
```

#### HTTP API(JSON 본문) 예시

```bash
curl -H "X-Vault-Token: ..." -H "Content-Type: application/json" \
  -X POST "$VAULT_ADDR/v1/github/roles/ci" \
  -d '{
    "installation_id": 12345678,
    "ttl": 3600,
    "max_ttl": 7200,
    "permissions": {
      "contents": "read",
      "issues": "write",
      "pull_requests": "write"
    }
  }'
```

## 자격 증명 읽기

```bash
vault read github/creds/ci
```

응답에 `token`, `expires_at`, `installation_id`, `permissions` 등이 포함됩니다. `vault lease revoke <lease_id>`를 수행하면 플러그인이 GitHub 토큰 무효화 엔드포인트를 호출해 `ghs_...` 토큰을 취소합니다(과거 발급분 중 리스에 토큰이 저장되지 않은 경우 GitHub 호출을 생략할 수 있음).

출력 예시:

```log
Key                Value
---                -----
lease_id           github/creds/ci/eCIXEkJKu1oHJIrWyHHpFKKr
lease_duration     59m59s
lease_renewable    false
expires_at         2026-04-06T01:43:33Z
installation_id    87654321
permissions        map[contents:write metadata:read]
token              ghs_...
```

토큰 확인 예시(레포 API 호출):

```bash
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/my-org/my-repo" | jq .
```

만료되었거나 잘못된 토큰이면 `401 Bad credentials`가 반환됩니다.

