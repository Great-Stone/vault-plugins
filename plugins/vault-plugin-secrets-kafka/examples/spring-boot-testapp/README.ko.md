# spring-boot-testapp (Vault Kafka Secret Engine 검증 UI)

`spring-boot-testapp`는 `vault-plugin-secrets-kafka`의 **동적(dynamic) / 정적(static) 자격 증명**을 발급받아 Kafka 접근(주로 produce)을 검증하는 **로컬 E2E 검증용 Spring Boot 웹앱**입니다.

## 목적

- Vault에서 Kafka 자격 증명을 발급받고, Kafka에 실제로 요청을 보내 **정상 동작 여부를 즉시 확인**
- 1초 주기 검증 + 최근 60건 히스토리로 **성공/실패 추이를 시각화**
- 동적/정적 역할(번들)을 UI에서 전환하며 **번들 비교/검증**

## 실행(로컬)

이 앱은 단독 실행보다는 `examples/docker-compose/` 스택에서 함께 검증하는 것을 전제로 합니다.

```bash
cd plugins/vault-plugin-secrets-kafka/examples/docker-compose
docker compose up -d --build
```

UI 접속:

- `http://localhost:8080`

## Vault 인증 방식(중요)

이 앱은 **Vault 토큰 기반(TOKEN)** 으로 동작합니다.

- `docker-compose`의 `vault-init` 컨테이너가 AppRole을 구성하고,
- `secret_id_num_uses=1`, `secret_id_ttl=10s` 조건에서 **AppRole 로그인 1회**로
- `token_period=60s`인 **periodic token**을 발급받아 configtree로 전달합니다.
- `spring-app`은 전달받은 `token`으로 Vault에 접근하고, Spring Cloud Vault가 **renew-self**를 통해 토큰을 주기적으로 갱신합니다.

즉, **AppRole은 “최초 로그인(토큰 발급)”에만 사용**되고, 앱은 이후 **token 갱신**으로만 세션을 유지합니다.

설정 파일:

- `src/main/resources/application.yaml`
  - `spring.cloud.vault.authentication: TOKEN`
  - `spring.cloud.vault.token: ${token:}` (`configtree`에서 주입)

## 검증 동작 개요

- UI에서 `kind(dynamic/static)` 및 `role`을 선택
- “1초 주기 시작”을 누르면 1초마다 다음을 수행
  - 필요 시 Vault에서 creds 재발급/갱신(리스 TTL/renewable에 따라 재사용/renew/reissue)
  - Kafka에 메시지 1건 produce
  - 성공/실패, 지연시간, 에러 종류(AUTH/TIMEOUT/DNS 등)를 히스토리에 기록

## Dynamic / Static 지원

- **Dynamic**: `kafka/roles/*`, `kafka/creds/*` (동적 SCRAM 발급)
- **Static**: `kafka/static-roles/*`, `kafka/static-creds/*` (정적 번들 반환; `plain`, `scram(+rotation)` 등)

> 예제 Kafka는 ACL authorizer를 사용하므로, 특정 principal이 토픽 접근을 하려면 ACL이 필요할 수 있습니다. `kafka-init`이 `app_plain`, `app_scram`용 ACL을 추가합니다.

## UI 구성

- **현재 설정/Role 선택**
  - `kind(dynamic/static)` + `role` 선택
  - role 정의를 간단히 표시(`auth_type`, `scram_mechanism`, `rotation_cron` 등)
- **제어 버튼**
  - “1초 주기 시작/일시정지”
  - “Renew(수동)”: 현재 lease를 renew 시도(불가능하면 reissue)
  - “Revoke(수동)”: 현재 lease revoke
- **원본 결과(JSON)**
  - Kafka produce의 원본 응답 메타데이터(토픽/파티션/오프셋/타임스탬프)
  - 실패 시 예외(stack) 포함
- **최근 60건 히스토리**
  - 성공/실패를 막대 그래프로 표시(우측이 최신)
- **이벤트 로그**
  - 최신 상태 변화/오류를 상단에 누적 표시

## 주요 REST API

- **설정/목록**
  - `GET /api/config`
  - `GET /api/roles`, `GET /api/role/{role}`
  - `GET /api/static-roles`, `GET /api/static-role/{role}`
- **상태/데이터**
  - `GET /api/status`
  - `GET /api/issued`
  - `GET /api/kafka/raw`
  - `GET /api/history`
- **제어**
  - `POST /api/polling/start` body: `{ "kind": "dynamic|static", "role": "..." }`
  - `POST /api/polling/stop`
  - `POST /api/renew`
  - `POST /api/revoke`

## 한계/주의사항

- 이 앱은 “운영용”이 아니라 **로컬 검증용**입니다.
- 검증은 현재 **Kafka produce 1건 성공 여부**를 기본 신호로 사용합니다(필요 시 consume/round-trip 검증으로 확장 가능).
