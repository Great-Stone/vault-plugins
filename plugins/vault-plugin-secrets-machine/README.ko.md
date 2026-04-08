# vault-plugin-secrets-machine

English documentation (영문): [`README.md`](./README.md)

원격 머신의 **로컬 OS 계정 비밀번호를 저장하고 주기적으로 변경(로테이션)**하는 HashiCorp Vault **시크릿 엔진 플러그인**입니다.

- **Linux**: **SSH**로 접속하여 `chpasswd`로 비밀번호 변경
- **Windows**: **WinRM**으로 PowerShell `Set-LocalUser` 실행

**정적(static) role만** 지원합니다(Database 정적 role과 유사). 동적 사용자 생성/삭제 기반 “dynamic role”은 범위에서 제외합니다.

## Vault 경로(요약)

플러그인을 `machine/`으로 마운트했다고 가정하면:

- `machine/config`: (레거시) 엔진 기본값(포트 등)
- `machine/config/<name>`: **연결 프로필(Database 엔진 스타일 config)** – SSH/WinRM 선택 + 비밀번호 변경용 관리자(root credential) 설정
- `machine/static-roles/<name>`: 정적 role 정의(한 role = 한 host + 한 username)
- `machine/static-creds/<role>`: 현재 번들 읽기(host, username, password, 로테이션 메타데이터)

## 요구사항

- Go(플러그인 빌드)
- Vault(플러그인 지원)
- Vault 플러그인 프로세스에서 대상 호스트로의 네트워크 연결(SSH/WinRM)

### 지원 Windows 버전

로테이션은 PowerShell `Set-LocalUser`를 사용합니다. 일반적으로 아래 버전에서 동작합니다.

- Windows 10 / 11
- Windows Server 2016 / 2019 / 2022

## 빌드

이 디렉터리에서:

```bash
cd plugins/vault-plugin-secrets-machine
make build
```

`make build`는 **Vault를 실행하는 머신(OS/아키텍처)**에 맞는 네이티브 바이너리를 생성합니다(macOS/Windows에서 호스트 Vault를 쓰는 경우 특히 중요). `exec format error`가 나오면 OS/아키가 맞지 않게 빌드된 경우가 많습니다.

Linux용(예: Vault가 Docker/리눅스에서 실행):

```bash
make build-linux
```

## 등록 및 활성화(예시)

Vault dev 모드로 실행하면서 `-dev-plugin-dir`에 플러그인 바이너리 디렉터리를 지정합니다:

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./dist
```

플러그인을 등록하고 secrets engine을 enable 합니다:

```bash
PLUGIN=$(realpath ./dist/vault-plugin-secrets-machine)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
export VAULT_TOKEN=root
export VAULT_ADDR=http://localhost:8200
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-machine
vault secrets enable -path=machine -plugin-name=vault-plugin-secrets-machine plugin
```

## 설정

### `machine/config` (선택)

`machine/config`는 role에서 `port` 등을 생략했을 때 적용할 기본값을 저장합니다.

| 필드 | 타입 | 예시 | 설명/주의 |
|---|---:|---|---|
| `default_ssh_port` | int | `22` | role에 `port`가 없을 때 SSH 기본 포트 |
| `default_winrm_port` | int | `5985` | role에 `port`가 없을 때 WinRM 기본 포트. `winrm_use_https=true`이고 `default_winrm_port`가 비어 있으면 `5986`을 사용 |
| `winrm_use_https` | bool | `true` | WinRM을 HTTPS로 사용(보통 5986) |
| `winrm_skip_tls_verify` | bool | `false` | WinRM TLS 검증 생략(**연구/PoC 용도에만**) |

예시:

```bash
vault write machine/config \
  winrm_use_https=true \
  winrm_skip_tls_verify=true
```

### 연결 프로필: `machine/config/<name>` (권장)

Vault Database secrets 엔진의 `config/<name>`처럼, **호스트/전송(SSH/WinRM) + “비밀번호 변경 권한이 있는 관리자 계정(root credential)”**을 `config/<name>`에 저장하고, 여러 `static-roles`가 이를 참조하도록 구성합니다.

#### 연결 프로필 필드

| 필드 | 타입 | 필수 | 예시 | 설명/주의 |
|---|---:|:---:|---|---|
| `name` | string | 예 | `win_lab` | 연결 프로필 이름 |
| `os` | string | 예 | `linux` / `windows` | SSH vs WinRM 선택 |
| `host` | string | 예 | `host.example.internal` | Vault 플러그인 프로세스에서 접근 가능해야 함 |
| `port` | int | 아니오 | `22` / `5985` / `5986` | 생략/0이면 기본값 사용 |
| `admin_username` | string | 예 | `vault-admin` | 대상 계정 비밀번호를 바꿀 수 있는 관리자 계정 |
| `admin_password` | string | 경우에 따라 | `...` | WinRM에는 필수. 또한 `root_rotation_cron`을 쓰려면 필수 |
| `admin_private_key` | string | 경우에 따라 | `-----BEGIN...` | Linux SSH 키 인증(PEM). 설정 시 admin_password 대신 사용 가능 |
| `admin_private_key_passphrase` | string | 아니오 | `...` | 키 passphrase(있는 경우) |
| `sudo_password` | string | 아니오 | `...` | Linux 전용(Passwordless sudo가 없을 때) |
| `winrm_use_https` | bool | 아니오 | `true` | Windows 전용 |
| `winrm_skip_tls_verify` | bool | 아니오 | `true` | Windows 전용(연구용) |
| `winrm_auth` | string | 아니오 | `basic` / `ntlm` | Windows 전용 |
| `root_rotation_cron` | string | 아니오 | `0 */12 * * *` | (선택) 관리자 계정(admin_username) 자체 비밀번호 로테이션. **admin_password 필요(`admin_private_key`와는 함께 사용할 수 없음)** |

예시(Linux 프로필 + SSH 키 인증):
```bash
vault write machine/config/linux_lab \
  name=linux_lab \
  os=linux \
  host="<linux-host>" \
  port=22 \
  admin_username="<ssh-admin>" \
  admin_private_key=@/path/to/id_rsa
```

예시(Windows 프로필 + 전용 관리자 계정):

```bash
vault write machine/config/win_lab \
  name=win_lab \
  os=windows \
  host="<windows-host>" \
  port=5985 \
  admin_username="vault-admin" \
  admin_password="<vault-admin-password>" \
  winrm_auth=basic
```

### 정적 role: `machine/static-roles/<name>`

정적 role 하나는 **“특정 로컬 계정”**을 의미하며, 반드시 `config/<name>` 연결 프로필을 참조해야 합니다.

- **username**: Vault가 비밀번호를 보관/로테이션하는 로컬 계정
- **config/<name>**: 대상 host/전송(SSH/WinRM) + 비밀번호 변경 권한이 있는 관리자(root credential) 설정
- **rotation_cron**: UTC cron 로테이션 주기

스케줄러는 일정 주기마다 due 여부를 확인하고, due이면 원격에서 비밀번호를 실제 변경한 뒤 Vault 저장소의 값을 갱신합니다. `static-creds`의 Vault 리스 TTL은 **로테이션 시점과 무관**하며, 응답은 “북키핑(lease bookkeeping)” 성격입니다.

#### 정적 role 필드

| 필드 | 타입 | 필수 | 예시 | 설명/주의 |
|---|---:|:---:|---|---|
| `config_name` | string | **예** | `win_lab` | `config/<name>`에서 host/전송/admin 설정을 상속 |
| `username` | string | **예** | `appuser` | 관리 대상 계정(비밀번호가 `static-creds`에 반환) |
| `validate_user_exists` | bool | 아니오 | `true` | 설정 시, role write 시점에 대상 호스트에 `username`이 실제로 존재하는지 검증합니다(`config/<name>`의 관리자 계정으로 조회). |
| `password` | string | 아니오 | `...` | 초기값(선택). 없으면 첫 로테이션 성공 후 채워짐 |
| `rotation_cron` | string | **예** | `0 */6 * * *` | UTC cron |

예시(Linux에서 테스트용 대상 계정 생성):

```bash
sudo useradd -m "<target-user>" || true
sudo passwd "<target-user>"
```

예시(Linux / SSH, 연결 프로필 기반):

```bash
vault write machine/static-roles/linux_app \
  config_name="linux_lab" \
  username="<target-user>" \
  validate_user_exists=true \
  rotation_cron="*/5 * * * *"
```

예시(Windows에서 테스트용 대상 계정 생성):

```powershell
New-LocalUser -Name "<target-user>" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
```

예시(Windows / WinRM, 연결 프로필 기반):

```bash
vault write machine/static-roles/win_app \
  config_name="win_lab" \
  username="<target-user>" \
  validate_user_exists=true \
  rotation_cron="0 */6 * * *"
```

## 자격증명 읽기

현재 번들을 읽습니다:

```bash
vault read machine/static-creds/linux_app
```

응답에는 다음이 포함됩니다:

- `host`, `port`, `username`
- `password`: Vault가 마지막으로 저장한 비밀번호(첫 로테이션 전엔 비어 있을 수 있음)
- `last_rotated_at`, `next_rotation_at`, `rotation_ttl`

## 보안/운영 주의사항

- **비밀 커밋 금지**: 실제 호스트/IP/계정/비밀번호를 저장소에 넣지 마세요. 문서/예제는 반드시 플레이스홀더만 사용하세요.
- **SSH host key 검증**: v1은 PoC 편의상 `InsecureIgnoreHostKey`를 사용합니다. 운영에서는 host key pinning/검증이 필요합니다.
- **WinRM TLS**: HTTPS + 정상 인증서를 권장합니다. `winrm_skip_tls_verify` / `winrm_insecure`는 연구용으로만 사용하세요.
- **권한 모델**: 로테이션을 위해서는 관리자 권한 계정이 필요합니다. `admin_password`(및 `sudo_password`)는 매우 민감 정보로 취급하세요.

## Windows 사전 준비(WinRM)

### 전용 로컬 관리자 계정 생성(vault-admin)

운영/검증 편의를 위해 built-in `Administrator` 대신 전용 계정(예: `vault-admin`)을 만들어 사용하는 것을 권장합니다.

관리자 PowerShell:

```powershell
New-LocalUser -Name "vault-admin" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
Add-LocalGroupMember -Group "Administrators" -Member "vault-admin"
```

Vault role에서는 `admin_username="vault-admin"`(필요 시 `.\vault-admin`)로 사용합니다.

### WinRM 활성화 및 Basic 허용(연구/PoC 용도)

WinRM 보안 설정은 환경별로 다릅니다. *연구/PoC* 기준으로는 보통 다음이 필요합니다.

- WinRM 활성화
- 방화벽에서 WinRM(5985/5986) 인바운드 허용
- Basic 인증 허용(HTTP Basic을 쓸 경우)
- HTTP(5985)를 쓸 때만 unencrypted 허용(가능하면 HTTPS 권장)

예시 PowerShell(관리자 권한):

```powershell
winrm quickconfig -q
winrm set winrm/config/service/auth '@{Basic="true"}'
winrm set winrm/config/service '@{AllowUnencrypted="true"}'
winrm set winrm/config/client/auth '@{Basic="true"}'
winrm set winrm/config/client '@{AllowUnencrypted="true"}'
```

HTTPS(권장) 사용 시에는 5986 리스너를 인증서로 구성하고, `AllowUnencrypted`는 꺼 둔 상태를 권장합니다.

