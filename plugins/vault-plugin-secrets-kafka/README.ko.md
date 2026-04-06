# vault-plugin-secrets-kafka

English documentation (영문): [`README.md`](./README.md)

Kafka 인증을 위한 HashiCorp Vault **시크릿 엔진 플러그인**입니다.

## Vault 경로(요약)

- `kafka/config`: Kafka 연결/관리자 부트스트랩 설정
- `kafka/roles/<name>`: **동적(dynamic)** role 정의(SCRAM 발급, TTL, 선택적 ACL 힌트)
- `kafka/creds/<role>`: **동적(dynamic)** 자격 증명 발급(응답에 `lease_id` + `data`)
- `kafka/static-roles/<name>`: **정적(static)** role 정의(저장된 번들 + 선택적 SCRAM rotation)
- `kafka/static-creds/<role>`: **정적(static)** 번들 반환(응답에 `lease_id` + `data`)

## 무엇을 제공하나요?

### 동적 SCRAM (v1)

`kafka/creds/<role>`을 읽으면 플러그인이 단기 SCRAM `username/password`를 발급하고(필요 시 ACL 생성),  
리스 revoke/만료 시 자격 증명을 무효화합니다(즉시 무효화를 위해 **password rotation을 우선 수행**하고, 이후 삭제를 best-effort로 시도).

### 정적 번들(기반)

정적(static) role은 “미리 준비된 자격 증명 번들(예: SASL/PLAIN, mTLS)을 Vault가 배포”하는 기반 모델입니다.  
정적 role은 기본적으로 Kafka 사용자/ACL을 생성/삭제하지 않고 **저장된 번들을 반환**합니다. 단, `auth_type=scram`일 때는 선택적으로 rotation 스케줄러를 통해 Kafka와 Vault 저장소의 비밀번호를 주기적으로 갱신할 수 있습니다.

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
VAULT_TOKEN=root
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-kafka
vault secrets enable -path=kafka -plugin-name=vault-plugin-secrets-kafka plugin
```

## 설정

### `kafka/config`

최소한 클라이언트용/관리자용 bootstrap endpoint를 설정해야 합니다.

로컬 Kafka를 다음과 같이 실행했다면:

```bash
cd examples/docker-compose
docker compose up kafka kafka-init
```

호스트에서 Vault CLI로 접속할 때(예제는 호스트 리스너를 노출):

```bash
vault write kafka/config \
  bootstrap_servers="localhost:19093" \
  admin_bootstrap_servers="localhost:19092"
```

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
단, `auth_type=scram`인 경우 선택적으로 scheduler가 Kafka Admin API로 비밀번호를 갱신하고, Vault 저장소의 `static_password`도 최신값으로 업데이트할 수 있습니다.

### 정적 role 필드

| 필드 | 타입 | 필수 | 예시 | 설명/주의 |
|---|---:|:---:|---|---|
| `name` | string | 예 | `app_plain` | role 이름 |
| `auth_type` | string | 예 | `plain` / `scram` / `mtls` | 번들 타입. rotation은 v1에서 `scram`만 지원 |
| `scram_mechanism` | string | scram만 | `SCRAM-SHA-256` | `SCRAM-SHA-256` 또는 `SCRAM-SHA-512` |
| `static_username` | string | 경우에 따라 | `my-user` | `plain`/`scram` 번들에서 사용. scram rotation이면 Kafka에 사용자 사전 존재 필요 |
| `static_password` | string | 경우에 따라 | `...` | `plain`/`scram` 번들에서 사용. scram rotation이면 scheduler가 갱신 |
| `static_props` | map | 아니오 | `security.protocol=SASL_SSL` | 추가 client properties(정적 번들로 반환) |
| `rotation_cron` | string | 아니오 | `*/5 * * * *` | scram password rotation cron(UTC) |
| `rotation_enabled` | bool | 아니오 | `true` | cron 설정 시 기본 true |
| `ttl` | seconds | 아니오 | `3600` | `kafka/static-creds/<role>` 기본 TTL |
| `max_ttl` | seconds | 아니오 | `7200` | TTL 상한(override 포함). `ttl` 이상이어야 함 |

### 정적 role 예시(plain)

정적 role은 Kafka 사용자/비밀번호를 외부에서 관리하고, Vault는 “배포만” 수행하려는 경우에 유용합니다.

```bash
vault write kafka/static-roles/app_plain \
  name="app_plain" \
  auth_type="plain" \
  static_username="my-user" \
  static_password="my-pass" \
  static_props="security.protocol=SASL_PLAINTEXT" \
  ttl=3600 \
  max_ttl=7200
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
  rotation_cron="*/1 * * * *" \
  rotation_enabled=true \
  ttl=3600 \
  max_ttl=7200
```

정적 번들 읽기:

```bash
vault read kafka/static-creds/app_scram
```

rotation이 설정되어 있으면 응답에 `last_rotated_at`, `next_rotation_at`(RFC3339)이 포함됩니다.

## 로컬 E2E 테스트(Docker Compose)

이 저장소에는 검증용 E2E 스택(Vault + Kafka + Spring Boot UI)이 포함되어 있습니다.

- 경로: `examples/docker-compose/`
- 실행:

```bash
cd plugins/vault-plugin-secrets-kafka
make build
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

### 예제 스택 관련 주의사항

- **SASL/PLAIN**을 검증하려면 브로커가 `PLAIN` 메커니즘을 활성화해야 합니다(예제 `server.properties`는 `SCRAM-SHA-256` + `PLAIN`을 활성화).
- **ACL authorizer**가 켜져 있고(예제는 StandardAuthorizer), 한 번이라도 ACL이 존재하면 principal별로 명시적 권한이 필요할 수 있습니다. 예제 `kafka-init`은 `app_plain`/`app_scram`용 principal에 `test-topic` RW 권한을 추가합니다.

## 참고

- OAuth/Kerberos는 **v1 스코프에서 제외**했습니다(대개 외부 IdP/KDC 의존).

