# vault-plugin-secrets-github

Vault 시크릿 엔진 플러그인으로, [GitHub App](https://docs.github.com/en/apps) 자격 증명(App ID + PEM 개인 키)을 사용해 [GitHub App 설치 액세스 토큰](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app)을 발급합니다.

## 제약: fine-grained PAT와 REST API

GitHub는 **fine-grained personal access token 문자열을 새로 생성하는 REST 엔드포인트**를 제공하지 않습니다. 조직의 `POST /orgs/{org}/personal-access-tokens` 등은 기존 토큰에 대한 **액세스 조정·취소** 용도입니다 ([REST API: personal access tokens](https://docs.github.com/en/rest/orgs/personal-access-tokens)).

자동화·동적 발급이 필요하면 GitHub가 권장하는 **GitHub App + 설치 액세스 토큰**을 사용하는 것이 맞으며, 본 플러그인은 그 흐름을 구현합니다. PAT가 반드시 필요하면 GitHub UI에서 생성한 뒤 Vault KV 등에 저장하는 방식을 검토하세요.

## 요구 사항

- Go 1.23+
- Vault 1.15+ (플러그인 multiplexing 사용; SDK `v0.5.4+` 호환)

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

## 등록 및 활성화 테스트

Vault Enterprise 바이너리를 사용하여 개발모드로 실행 합니다. `-dev-plugin-dir`옵션으로 플러그인 디렉터리를 지정합니다.

```bash
# 버전 확인 : vault version
Vault v1.21.4+ent (58daf12e183c5833504198f65644c867a68e9c2f), built 2026-03-04T09:11:58Z
```

```bash
# Dev 모드로 서버 실행 시 플러그인 디렉토리 지정
vault server -dev -dev-root-token-id=root -dev-listen-address=0.0.0.0:8200 -dev-plugin-dir=./bin
```

```log
# Log
The following dev plugins are registered in the catalog:
    - vault-plugin-secrets-github
```

SHA256을 계산해 카탈로그에 등록합니다.

```bash
PLUGIN=$(realpath ./bin/vault-plugin-secrets-github)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-github
vault secrets enable -path=github -plugin-name=vault-plugin-secrets-github plugin
```



## 설정

### `github/config`


| 필드            | 설명                                                                                                            |
| ------------- | ------------------------------------------------------------------------------------------------------------- |
| `app_id`      | GitHub App ID (필수)                                                                                            |
| `private_key` | App의 PEM RSA 개인 키 (필수)                                                                                        |
| `base_url`    | GitHub Enterprise Server 사용 시 API 베이스 (예: `https://github.example.com/api/v3`). 생략 시 `https://api.github.com` |


#### GitHub 준비(UI·문서)

`app_id`와 `private_key`는 **GitHub에서 GitHub App을 등록하고 개인 키를 발급**할 때 얻습니다. Vault 전용 UI는 없으며, 아래 GitHub 문서를 따르면 됩니다.

- [GitHub App 등록(About creating GitHub Apps)](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps)
- [GitHub App 등록 절차(Registering a GitHub App)](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app) — Developer settings에서 앱을 만들고 **App ID** 확인
- [GitHub App 개인 키 생성·관리(Managing private keys for GitHub Apps)](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps) — PEM을 내려받아 Vault에 넣을 값으로 사용
- [GitHub App 설치(Installing your own GitHub App)](https://docs.github.com/en/apps/creating-github-apps/installing-github-apps/installing-your-own-github-app) — 설치 후 **installation_id**를 역할에 쓰게 됨(별도 단계)

GitHub.com이 아니라 **GitHub Enterprise Server**를 쓰는 경우에도 조직 설정·앱 메뉴에서 동일한 개념으로 App을 만들 수 있으며, 이때 `base_url`을 해당 인스턴스의 API 베이스로 맞춥니다.

##### 로컬에서 Vault를 써도 GitHub App 등록이 되나요?

**됩니다.** GitHub App 등록은 [GitHub.com(또는 Enterprise) 웹](https://github.com/settings/apps/new)에서만 이루어지며, **Vault가 `localhost`인지·클러스터인지와 무관**합니다. 이 플러그인은 Vault가 **아웃바운드로 GitHub REST API**(`api.github.com` 등)를 호출해 설치 토큰을 만들 뿐이고, GitHub가 사용자의 PC나 Vault 주소로 **인바운드 접속할 필요는 없습니다.** (웹훅을 켜 두었다면 그때만 공개 URL이 필요합니다. 아래 참고.)

##### Create GitHub App 폼 작성 가이드(필드가 많을 때)

웹에서 앱을 만들 때는 보통 **Settings → Developer settings → GitHub Apps → New GitHub App** 또는 [Create GitHub App](https://github.com/settings/apps/new)으로 들어갑니다. 조직 소유 앱은 `https://github.com/organizations/<ORG>/settings/apps/new` 형태입니다.

이 플러그인은 **설치 액세스 토큰(서버 간)** 만 사용하므로, 아래처럼 최소한으로 맞추면 됩니다.

| 구간 | 실무 팁 |
| ---- | ------ |
| GitHub App name | 조직 내에서 구분되는 이름(예: `my-vault-plugin-secrets-github`). |
| Description | 자유(내부용 표기). |
| Homepage URL | 필수로 요구됩니다. 공개 레포·조직 사이트·문서 URL 등 **실제로 열리는 HTTPS URL**을 넣는 것이 안전합니다. |
| Webhook | `Active`를 비활성화 합니다. |
| Permissions | [앱 권한](https://docs.github.com/en/apps/creating-github-apps/setting-permissions-for-github-apps)은 Vault에서 생성할 GitHub 토큰으로 줄 수 있는 권한의 상한입니다. `vault write github/roles/... permissions=...`에 넣는 값은 여기서 허용한 범위 안이어야 합니다. 예: 코드 읽기만이면 Repository → **Contents: Read and Write**, 메타데이터는 **Metadata: Read-only** 등. 필요한 만큼만 설정합니다. |
| **Where can this GitHub App be installed?** | **Only on this account**에 체크하고, 여러 조직에 설치하려면 **Any account** 등으로 조정합니다. |

#### Vault 구성 필요 값 확인

등록 후 `About`에서 `App ID`를 확인하고, `Private keys`에서 PEM을 생성·다운로드한 뒤 Vault `github/config`의 `private_key`로 넣습니다. 이어서 앱을 **Install**하고 설치 화면에서 **installation id**(URL 또는 API)를 확인해 역할에 사용합니다.

### GitHub Secret Engine 구성

```bash
vault write github/config \
  app_id=123456 \
  private_key=@/path/to/app-private-key.pem \
  base_url="https://api.github.com"
```

### GitHub Secret Engine 역할 구성

#### installation_id 확인 절차

역할의 `installation_id`는 **이미 GitHub에 앱을 설치한 뒤**에 진행합니다.

   - GitHub.com → **Settings**(개인) 또는 **조직 Settings** → **Developer settings** → **GitHub Apps** → 해당 앱 → **Install App** / 이미 설치됨인 경우 **Configure**.  
   - 설치 구성 페이지로 이동하면 주소창 URL에 `.../installations/<숫자>` 형태가 자주 보입니다. 이 **`<숫자>`가 `installation_id`**입니다.  
   - 예: `https://github.com/settings/installations/12345678` → `12345678`  
   - 조직 설치는 `https://github.com/organizations/<ORG>/settings/installations/<숫자>` 형태일 수 있습니다.

| 필드 | 설명 |
| ----------------- | ----------------- |
| `installation_id` | 해당 App의 **설치 ID**(필수). GitHub가 설치 시마다 부여하는 정수이며, **임의로 만들 수 없습니다.** 다만 Vault는 GitHub에 자동 질의하지 않으므로, 운영자가 GitHub UI/API에서 확인한 값을 **역할에 반드시 넣어야** 합니다(생략 불가). |
| `repositories`    | 선택. 저장소 이름 목록. CLI에서는 **쉼표로 구분**합니다. `owner/repo` 형태로 적어도 되며, 플러그인은 GitHub API에 맞게 **`/` 뒤의 짧은 이름만** 보냅니다([공식 예시](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app)와 동일). |
| `repository_ids`  | 선택. 저장소 **숫자 ID** 목록. CLI에서도 **쉼표로 구분**합니다. |
| `permissions`     | 선택. 설치 토큰에 부여할 [권한 맵](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app). 키는 REST 스펙의 저장소 권한 이름, 값은 보통 `read` 또는 `write`. |
| `ttl` / `max_ttl` | 기본 TTL 및 상한(초) |

**왜 “GitHub가 부여한다”고 하면서도 역할에 넣나요?** “부여한다”는 말은 **값의 출처가 GitHub**라는 뜻이지, **입력하지 않아도 된다**는 뜻이 아닙니다. 발급 API가 `POST /app/installations/{installation_id}/access_tokens` 형태이므로, 플러그인은 “어느 설치에 대한 토큰인지”를 알기 위해 역할에 그 숫자를 저장해야 합니다. Vault가 설치 목록을 대신 조회해 주지는 않습니다(향후 확장은 별도).

#### `repositories`와 `repository_ids`를 같이 쓸 수 있나요?

- 이 플러그인은 역할에 저장된 값을 그대로 GitHub API 요청 본문(`repositories`, `repository_ids`)에 **둘 다** 넣을 수 있습니다(비어 있지 않을 때만 직렬화).
- GitHub 쪽에서 두 필드를 동시에 어떻게 해석하는지(병합·우선순위·검증 오류 등)는 **[Create an installation access token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app)** 스펙과 API 버전에 따릅니다. 운영에서는 보통 **이름만** 또는 **ID만** 한 가지 방식으로 지정해 범위를 명확히 하는 편이 안전합니다.

#### CLI에서 `repositories` 쉼표 구분 예

```bash
vault write github/roles/ci \
  installation_id=87654321 \
  repositories="my-org/my-repo-a,my-org/my-repo-b" \
  permissions=contents=write \
  ttl=3600 \
  max_ttl=7200
```

#### `permissions` 예시와 여러 개 넣기

권한 키·허용 값은 GitHub REST 문서의 요청 본문 설명과 동일합니다. 저장소 수준 예:


| 키 (예)           | 값 (예)            | 용도 요약                 |
| --------------- | ---------------- | --------------------- |
| `metadata`      | `read`           | 메타데이터·협업자 목록 등(자주 필요) |
| `contents`      | `read` / `write` | 코드·커밋·브랜치             |
| `issues`        | `read` / `write` | 이슈                    |
| `pull_requests` | `read` / `write` | PR                    |
| `actions`       | `read` / `write` | Actions 워크플로          |
| `secrets`       | `read` / `write` | 저장소 시크릿               |


**Vault CLI**에서 키·값 쌍을 여러 개 주려면 같은 필드를 반복합니다.

```bash
vault write github/roles/ci \
  installation_id=12345678 \
  ttl=3600 \
  max_ttl=7200 \
  permissions=contents=read \
  permissions=issues=write \
  permissions=pull_requests=write
```

**HTTP API**로 한 번에 객체를 보내려면 JSON 본문을 사용합니다.

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

단일 권한만 줄 때의 최소 예:

```bash
vault write github/roles/ci \
  installation_id=12345678 \
  ttl=3600 \
  max_ttl=7200 \
  permissions=contents=read
```

### 자격 증명 읽기

```bash
vault read github/creds/ci
```

응답 데이터에 `token`, `expires_at`, `installation_id`, `permissions`가 포함됩니다. `vault lease revoke <lease_id>` 시 플러그인이 GitHub에 `DELETE /installation/token`을 호출해 해당 `ghs_` 토큰을 무효화합니다(리스에 토큰이 없는 예전 발급분은 GitHub 호출을 생략할 수 있음).

```log
# 출력 예시
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

확인을 위한 레포 정보 조회 API 예시

```bash
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/my-org/my-repo" | jq .
```

`lease_id`로 `vault lease revoke` 하거나, 만료 후에는 토큰이 자연 만료됩니다.

```bash
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/my-org/my-repo" | jq .

# 잘못된·만료된 토큰으로 호출 시 예시
{
  "message": "Bad credentials",
  "documentation_url": "https://docs.github.com/rest",
  "status": "401"
}
``` 