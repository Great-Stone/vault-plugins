# vault-plugin-secrets-kafka

English documentation (영문): [`README.md`](./README.md)

Kafka 인증을 위한 HashiCorp Vault **시크릿 엔진 플러그인**입니다.

## Vault 경로(요약)

- `kafka/config`: Kafka 연결/관리자 부트스트랩 설정
- `kafka/roles/<name>`: **동적(dynamic)** role 정의(SCRAM 발급, TTL, 선택적 ACL 힌트)
- `kafka/creds/<role>`: **동적(dynamic)** 자격 증명 발급(응답에 `lease_id` + `data`)
- `kafka/static-roles/<name>`: **정적(static)** role 정의(저장된 번들; SCRAM은 `rotation_cron` 필수)
- `kafka/static-creds/<role>`: **정적(static)** 번들 반환(응답에 `lease_id` + `data`)

## 무엇을 제공하나요?

### 동적 SCRAM (v1)

`kafka/creds/<role>`을 읽으면 플러그인이 단기 SCRAM `username/password`를 발급하고(필요 시 ACL 생성),  
리스 revoke/만료 시 자격 증명을 무효화합니다(즉시 무효화를 위해 **password rotation을 우선 수행**하고, 이후 삭제를 best-effort로 시도).

### 정적 번들(기반)

정적(static) role은 “미리 준비된 자격 증명 번들(예: SASL/PLAIN, mTLS)을 Vault가 배포”하는 기반 모델입니다.  
정적 role은 Kafka 사용자/ACL을 생성/삭제하지 않고 **저장된 번들을 반환**합니다. `auth_type=scram`이면 **`rotation_cron`에 따라** Kafka와 Vault 저장소의 비밀번호를 주기적으로 갱신합니다(Vault `static-creds` 리스 TTL과 무관).

## 요구사항

- Go(플러그인 빌드)
- Vault(플러그인 지원; 로컬 스택은 Vault `1.21` 기준)
- Kafka(로컬 스택은 `apache/kafka:4.2.0` 기준)

## 빌드

이 디렉터리에서:

```bash
cd plugins/vault-plugin-secrets-kafka
make build
```

`make build`는 **Vault를 실행하는 OS/아키텍처용 네이티브** 바이너리를 만듭니다(macOS/Windows에서 호스트 Vault를 쓸 때 필요). `fork/exec ... exec format error`가 나면 Vault가 돌아가는 환경과 플러그인 바이너리 OS/아키가 맞지 않은 경우가 많습니다(예: Linux용으로 빌드했는데 macOS에서 Vault 실행).

**Docker Compose** 예제에 넣을 플러그인은 Linux용으로 빌드합니다. 빌드 머신의 `go env GOARCH`와 맞추면 일반적인 Docker Desktop 환경(Apple Silicon은 arm64, Intel은 amd64)과 맞습니다:

```bash
make build-linux
```

직접 Go로 빌드하는 예시:

```bash
go build -o dist/vault-plugin-secrets-kafka ./cmd/vault-plugin-secrets-kafka
```

## 등록 및 활성화(예시)

Vault dev 모드로 실행하면서 `-dev-plugin-dir`에 플러그인 바이너리 경로를 지정합니다:

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./dist
```

플러그인을 등록하고 secrets engine을 enable 합니다:

```bash
PLUGIN=$(realpath ./dist/vault-plugin-secrets-kafka)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
export VAULT_TOKEN=root
export VAULT_ADDR=http://localhost:8200
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-kafka
vault secrets enable -path=kafka -plugin-name=vault-plugin-secrets-kafka plugin
```

## 설정

### `kafka/config`

발급되는 **클라이언트**용 부트스트랩(`bootstrap_servers`)과, 플러그인이 SCRAM/ACL 등 **관리 API**를 호출할 때 쓰는 **관리자** 부트스트랩(`admin_bootstrap_servers`)을 설정합니다. `vault write kafka/config`는 Vault에 설정을 저장할 뿐이며, **이 명령 자체가 Kafka에 로그인하지는 않습니다** — Kafka 인증은 플러그인이 나중에 연결할 때 적용됩니다. Vault 쪽에서는 유효한 토큰과 `kafka/config` 쓰기 권한이 필요합니다.

| 필드 | 설명 |
|---|---|
| `bootstrap_servers` | 애플리케이션 클라이언트용 브로커 주소(발급 응답에 포함). |
| `admin_bootstrap_servers` | Admin API용 브로커(비어 있으면 `bootstrap_servers`와 동일하게 취급). |
| `security_protocol` | 선택. 운영 환경에서는 관리 연결에 `SASL_PLAINTEXT` 또는 `SASL_SSL`을 쓰는 경우가 많으며, 이때 `admin_username` / `admin_password`가 필요합니다. |
| `sasl_mechanism` | 관리 클라이언트 SASL 메커니즘(예: `SCRAM-SHA-256`). |
| `admin_username` / `admin_password` | 관리 API용 Kafka principal. `security_protocol`이 SASL일 때 플러그인에서 필수입니다. |

**운영 환경**에서는 클라이언트·관리 트래픽 모두 인증을 요구하는 경우가 많습니다. 관리용 principal에는 SCRAM 사용자 생성/삭제·ACL 변경 등 필요한 최소 권한만 주는 것이 좋습니다. 이 저장소의 Docker Compose 예제는 `kafka-init`에서 SCRAM 사용자 `vault_admin`을 만들고, `server.properties`의 `super.users`로 데모용 관리 권한을 부여한 뒤, 동일 비밀번호를 `VAULT_KAFKA_ADMIN_PASSWORD`로 Vault의 `kafka/config`에 넘깁니다. 실제 운영에서는 `super.users` 대신 ACL/역할 기반 최소 권한을 검토하세요.

로컬 스택의 Kafka에 **호스트에서** 접속할 때(포트 `19092` / `19093`):

```bash
cd examples/docker-compose
docker compose up -d kafka kafka-init
```

```bash
vault write kafka/config \
  bootstrap_servers="localhost:19093" \
  admin_bootstrap_servers="localhost:19093" \
  security_protocol="SASL_PLAINTEXT" \
  sasl_mechanism="SCRAM-SHA-256" \
  admin_username="vault_admin" \
  admin_password="vault-admin-demo-secret"
```

Compose 기본값과 맞추려면 `admin_password`는 `VAULT_KAFKA_ADMIN_PASSWORD`와 동일하게 두면 됩니다. (연구용으로만) 관리 전용 PLAINTEXT 리스너를 쓰고 SASL을 쓰지 않는 구성에서는 `security_protocol`을 SASL로 두지 않고 `admin_username`/`admin_password` 없이 연결하는 패턴도 가능합니다.

### 동적 role: `kafka/roles/<name>`

동적 role은 `kafka/creds/<role>`에서 **단기 SCRAM 자격 증명**을 발급하는 방법을 정의합니다.

#### 동적 role 필드

| 필드 | 타입 | 필수 | 예시 | 설명/주의 |
|---|---:|:---:|---|---|
| `name` | string | 예 | `ci` | role 이름(경로의 `<name>`과 동일하게 두는 것을 권장) |
| `scram_mechanism` | string | 아니오 | `SCRAM-SHA-256` | `SCRAM-SHA-256` 또는 `SCRAM-SHA-512` (기본: `SCRAM-SHA-256`) |
| `topic` | string | 아니오 | `test-topic` | v1 최소 ACL 힌트(토픽 read/write/describe) |
| `group_prefix` | string | 아니오 | `vaulttest-` | v1 최소 ACL 힌트(consumer group read/describe, **prefix 패턴**) |
| `ttl` | seconds | 아니오 | `600` | 발급(`kafka/creds/<role>`) 기본 TTL |
| `max_ttl` | seconds | 아니오 | `3600` | TTL 상한(override 포함). `ttl` 이상이어야 함 |

#### 동적 role 예시(SCRAM)

```bash
vault write kafka/roles/ci \
  name="ci" \
  scram_mechanism="SCRAM-SHA-256" \
  topic="test-topic" \
  group_prefix="vaulttest-" \
  ttl=600 \
  max_ttl=3600
```

## 자격 증명 읽기

동적 자격 증명 발급:

```bash
vault read kafka/creds/ci
```

출력 예시:

```log
Key                  Value
---                  -----
lease_id             kafka/creds/ci/4MhUj69WMxASYEHxZMA1b43v
lease_duration       10m
lease_renewable      false
auth_type            scram
bootstrap_servers    localhost:19093
group_prefix         vaulttest-
mode                 dynamic
password             NjVlkTkrkyqpbxWyEBQjfOH5zqqREvwS
sasl_mechanism       SCRAM-SHA-256
security_protocol    SASL_PLAINTEXT
topic                test-topic
username             vault_ci_6A3cP2lF
```

응답에는 `lease_id`와 `data`가 포함됩니다(예: `bootstrap_servers`, `username`, `password`, protocol/mechanism 필드). revoke:

```bash
vault lease revoke <lease_id>
```

요청별 TTL override도 가능합니다(단, `max_ttl` 이하여야 함):

```bash
vault read kafka/creds/ci ttl=120
```

## 정적 role 및 static-creds

정적 role은 `static-roles/` 아래에 설정하고 `static-creds/`로 읽습니다. 정적 role은 Kafka 사용자/ACL을 생성/삭제하지 않고 **저장된 번들을 반환**합니다.  
`auth_type=scram`이면 **`rotation_cron`을 반드시 지정**하고, scheduler가 해당 주기(UTC)로 Kafka 비밀번호와 Vault 저장소의 `static_password`를 갱신합니다. **비밀번호 갱신 시점은 Vault 리스 TTL과 무관**합니다.

`kafka/static-creds/` 응답에 붙는 Vault 리스 TTL은 **역할별로 설정하지 않으며**, 플러그인 고정값(기본 TTL 3600초, max TTL 86400초)을 사용합니다.

### 정적 role 필드

| 필드 | 타입 | 필수 | 예시 | 설명/주의 |
|---|---:|:---:|---|---|
| `name` | string | 예 | `app_plain` | role 이름 |
| `auth_type` | string | 예 | `plain` / `scram` / `mtls` | 번들 타입. 주기적 rotation은 v1에서 `scram`만 |
| `scram_mechanism` | string | scram만 | `SCRAM-SHA-256` | `SCRAM-SHA-256` 또는 `SCRAM-SHA-512` |
| `static_username` | string | 경우에 따라 | `my-user` | `plain`/`scram` 번들. `scram`이면 Kafka에 사용자 사전 존재 필요 |
| `static_password` | string | 경우에 따라 | `...` | `plain`/`scram` 번들. `scram`이면 scheduler가 rotation 후 갱신 |
| `static_props` | map | 아니오 | `security.protocol=SASL_SSL` | 추가 client properties(정적 번들로 반환) |
| `rotation_cron` | string | **scram이면 필수** | `*/5 * * * *` | 비밀번호 rotation cron(UTC). `plain`/`mtls`에서는 무시 |

### 정적 role 예시(plain)

정적 role은 Kafka 사용자/비밀번호를 외부에서 관리하고, Vault는 “배포만” 수행하려는 경우에 유용합니다.

```bash
vault write kafka/static-roles/app_plain \
  name="app_plain" \
  auth_type="plain" \
  static_username="my-user" \
  static_password="my-pass" \
  static_props="security.protocol=SASL_PLAINTEXT"
```

### 정적 role 예시(SCRAM + rotation)

전제:

- Kafka에 SCRAM이 활성화되어 있어야 합니다(예제 compose는 SCRAM-SHA-256 활성화).
- `static_username` 사용자는 Kafka에 사전에 존재해야 합니다(최초 1회는 외부에서 생성하거나 out-of-band로 생성).

```bash
vault write kafka/static-roles/app_scram \
  name="app_scram" \
  auth_type="scram" \
  scram_mechanism="SCRAM-SHA-256" \
  static_username="my-scram-user" \
  static_password="initial-password" \
  rotation_cron="*/1 * * * *"
```

정적 번들 읽기:

```bash
vault read kafka/static-creds/app_scram
```

`scram` role의 응답에는 `last_rotated_at`, `next_rotation_at`(RFC3339)이 포함됩니다.

## kcat으로 확인하기 (옵션)

전체 스택을 올리기 전·후에, 호스트에서 [kcat](https://github.com/edenhill/kcat)(예전 이름 **kafkacat**)으로 브로커를 점검할 수 있습니다. Kafka용 경량 CLI 프로듀서/컨슈머입니다.

**설치 예시**

- **macOS (Homebrew):** `brew install kcat`
- **Debian/Ubuntu:** `apt install kcat`(배포판에 따라 패키지/바이너리 이름이 `kafkacat`인 경우도 있음)
- **그 외:** [kcat 릴리스](https://github.com/edenhill/kcat/releases)를 참고하거나, 로컬에 바이너리를 설치하지 않으려면 공개된 `kcat` 컨테이너 이미지를 사용할 수 있습니다

Compose의 Kafka가 떠 있을 때(`examples/docker-compose`에서 `kafka` + `kafka-init`) 브로커는 호스트에 `localhost:19092`(PLAINTEXT), `localhost:19093`(SASL/SCRAM)로 노출됩니다. PLAINTEXT로 메타데이터/토픽 목록을 보려면:

```bash
kcat -b localhost:19092 -L
```

**SCRAM-SHA-256**으로 `test-topic`을 **구독**하려면(`vault read kafka/creds/<role>` 또는 정적 번들에서 받은 사용자/비밀번호로 `USER`/`PASS` 대체):

```bash
kcat -b localhost:19093 \
  -X security.protocol=SASL_PLAINTEXT \
  -X sasl.mechanism=SCRAM-SHA-256 \
  -X sasl.username=<username> \
  -X sasl.password=<password> \
  -t test-topic -C -o beginning
```

동일한 SASL 설정으로 `test-topic`에 `hello` 메시지 프로듀스 예시:

```bash
echo "hello" | kcat -b localhost:19093 \
  -X security.protocol=SASL_PLAINTEXT \
  -X sasl.mechanism=SCRAM-SHA-256 \
  -X sasl.username=<username> \
  -X sasl.password=<password> \
  -t test-topic -P
```

## 로컬 E2E 테스트(Docker Compose)

이 저장소에는 검증용 E2E 스택(Vault + Kafka + Spring Boot UI)이 포함되어 있습니다.

- 경로: `examples/docker-compose/`
- 실행:

```bash
cd plugins/vault-plugin-secrets-kafka
make build-linux
cp -f dist/vault-plugin-secrets-kafka examples/docker-compose/vault/plugins/vault-plugin-secrets-kafka

cd examples/docker-compose
docker compose up -d --build
```

검증 UI:

- `http://localhost:8080`

스택 구성:

- **Vault**: `hashicorp/vault:1.21` (dev 모드, `-dev-plugin-dir=/vault/plugins`)
- **Kafka**: `apache/kafka:4.2.0` (`server.properties` 마운트 기반 설정)
- **vault-init**: 플러그인 등록/enable, `kafka/config` 작성, role 생성, 테스트용 정책/인증 정보(configtree) 생성
- **spring-app**: Spring Cloud Vault(Config Data)로 Vault에 접속하여 동적/정적 번들을 선택해 1초 주기 produce 검증

**Kafka 관리 계정(compose):** `kafka-init`이 SCRAM 사용자 `vault_admin`을 만들고(비밀번호는 `VAULT_KAFKA_ADMIN_PASSWORD`, 기본 `vault-admin-demo-secret`), 브로커 `super.users`에 `User:vault_admin`을 넣어 플러그인이 쓰는 Admin API(SCRAM·ACL 등)가 데모에서 동작하도록 했습니다. `vault-init`은 동일 비밀번호를 `kafka/config`의 `admin_username`/`admin_password`와 `SASL_PLAINTEXT`+`SCRAM-SHA-256`에 기록합니다. 운영에서는 환경 변수로 강한 비밀번호를 쓰고, `super.users` 대신 ACL 기반 최소 권한을 권장합니다.

### 예제 스택 관련 주의사항

- **SASL/PLAIN**을 검증하려면 브로커가 `PLAIN` 메커니즘을 활성화해야 합니다(예제 `server.properties`는 `SCRAM-SHA-256` + `PLAIN`을 활성화).
- **ACL authorizer**가 켜져 있고(예제는 StandardAuthorizer), 한 번이라도 ACL이 존재하면 principal별로 명시적 권한이 필요할 수 있습니다. 예제 `kafka-init`은 `app_plain`/`app_scram`용 principal에 `test-topic` RW 권한을 추가합니다.
- **Vault 플러그인 관리 계정**: `kafka-init`이 SCRAM 사용자 `vault_admin`을 만들고, 브로커 `super.users`에 등록합니다. `vault-init`은 동일 비밀번호로 `kafka/config`에 SASL 관리 클라이언트를 설정합니다. 운영에서는 강한 비밀번호와 최소 권한 모델을 사용하세요.

## 참고

- OAuth/Kerberos는 **v1 스코프에서 제외**했습니다(대개 외부 IdP/KDC 의존).
