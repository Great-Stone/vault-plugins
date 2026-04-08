# vault-plugin-secrets-machine

Korean documentation (한국어): [`README.ko.md`](./README.ko.md)

A HashiCorp Vault **secrets engine plugin** that stores and rotates passwords for **local OS accounts** on remote machines:

- **Linux**: rotate via **SSH** (`chpasswd`)
- **Windows**: rotate via **WinRM** (PowerShell `Set-LocalUser`)

**static roles only** (Database static role analogy). Dynamic user provisioning is out of scope.

## Vault paths (high-level)

Assuming you mount the plugin at `machine/`:

- `machine/config`: legacy optional engine defaults (ports, WinRM HTTPS/TLS behavior)
- `machine/config/<name>`: **connection profile** (Database-like config) – stores transport + privileged credential used to rotate accounts
- `machine/static-roles/<name>`: static role definition (one role = one host + one username)
- `machine/static-creds/<role>`: read current credential bundle (host, username, password, rotation metadata)

## Requirements

- Go (see `go.mod` under this plugin; build uses the standard Go toolchain)
- Vault with plugin support
- Network reachability from the Vault plugin process to target hosts (SSH/WinRM)

## Build

From this directory:

```bash
cd plugins/vault-plugin-secrets-machine
make build
```

`make build` produces a **native** binary for the machine running `vault` (required when Vault runs on macOS or Windows). If you see `exec format error`, the plugin binary was built for the wrong OS/arch.

For Linux (e.g., Vault runs in Docker or on a Linux host):

```bash
make build-linux
```

## Register & enable (example)

Run Vault in dev mode and point `-dev-plugin-dir` to the directory containing the plugin binary:

```bash
vault server -dev \
  -dev-root-token-id=root \
  -dev-listen-address=0.0.0.0:8200 \
  -dev-plugin-dir=./dist
```

Register the plugin and enable the secrets engine:

```bash
PLUGIN=$(realpath ./dist/vault-plugin-secrets-machine)
SHA256=$(shasum -a 256 "$PLUGIN" | awk '{print $1}')
export VAULT_TOKEN=root
export VAULT_ADDR=http://localhost:8200
vault plugin register -sha256="$SHA256" secret vault-plugin-secrets-machine
vault secrets enable -path=machine -plugin-name=vault-plugin-secrets-machine plugin
```

## Configure

### `machine/config` (optional)

`machine/config` stores optional defaults applied when a role omits values.

| Field | Type | Example | Notes |
|---|---:|---|---|
| `default_ssh_port` | int | `22` | Default SSH port when a role omits `port`. |
| `default_winrm_port` | int | `5985` | Default WinRM port when a role omits `port`. If `winrm_use_https=true` and `default_winrm_port` is unset, the plugin uses `5986`. |
| `winrm_use_https` | bool | `true` | Use HTTPS for WinRM (typically 5986). |
| `winrm_skip_tls_verify` | bool | `false` | Skip TLS verification for WinRM (**lab only**). |

Example:

```bash
vault write machine/config \
  winrm_use_https=true \
  winrm_skip_tls_verify=true
```

### Connection profiles: `machine/config/<name>` (recommended)

This is the **Database secrets engine style**: put transport + privileged credential in `config/<name>`, then reference it from many static roles.

#### Connection profile fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `name` | string | yes | `prod-win` | Config profile name. |
| `os` | string | yes | `linux` / `windows` | Selects SSH vs WinRM. |
| `host` | string | yes | `host.example.internal` | Reachable from the Vault plugin process. |
| `port` | int | no | `22` / `5985` / `5986` | Optional; 0 uses defaults. |
| `admin_username` | string | yes | `vault-admin` | Privileged account used to change passwords. |
| `admin_password` | string | depends | `...` | Required for WinRM and for `root_rotation_cron`. For Linux, can be omitted if using `admin_private_key`. |
| `admin_private_key` | string | depends | `-----BEGIN...` | Linux SSH key auth (PEM). Preferred over password. |
| `admin_private_key_passphrase` | string | no | `...` | Optional passphrase. |
| `sudo_password` | string | no | `...` | Linux only (if not passwordless sudo). |
| `winrm_use_https` | bool | no | `true` | Windows only. |
| `winrm_skip_tls_verify` | bool | no | `true` | Windows only (lab only). |
| `winrm_auth` | string | no | `basic` / `ntlm` | Windows only. |
| `root_rotation_cron` | string | no | `0 */12 * * *` | Optional: rotate `admin_username` password on schedule (**requires `admin_password`**). |

Example (Windows profile + dedicated admin):

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

### Static roles: `machine/static-roles/<name>`

A static role represents **one local OS account on one host**:

- **host**: the machine address
- **username**: the local account whose password Vault stores/rotates
- **admin_username/admin_password or admin_private_key**: privileged account used to perform the password change
- **rotation_cron**: UTC cron schedule for rotation

The scheduler checks due roles periodically and rotates when due. Rotation timing is independent from Vault lease TTL returned by `static-creds` reads.

#### Static role fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `config_name` | string | no | `win_lab` | If set, inherits host/transport/admin settings from `config/<name>`. |
| `os` | string | yes | `linux` / `windows` | Transport is selected by OS. |
| `host` | string | yes | `host.example.internal` | Must be reachable from the Vault plugin process. |
| `port` | int | no | `22` / `5985` / `5986` | If omitted/0, defaults come from `machine/config` or built-ins. |
| `username` | string | yes | `appuser` | Managed account (password returned on `static-creds`). |
| `password` | string | no | `...` | Optional initial value; otherwise set after first successful rotation. |
| `admin_username` | string | yes | `vaultadmin` | Privileged account for SSH/WinRM. |
| `admin_password` | string | depends | `...` | Privileged account password (SSH/WinRM). For Linux, you can use `admin_private_key` instead. |
| `admin_private_key` | string | depends | `-----BEGIN...` | Linux SSH PEM private key. If set, it is preferred over `admin_password`. |
| `admin_private_key_passphrase` | string | no | `...` | Optional passphrase for the private key. |
| `sudo_password` | string | no | `...` | Linux only: used for `sudo -S` if passwordless sudo is not available. |
| `winrm_insecure` | bool | no | `true` | Per-role TLS skip (combined with `winrm_skip_tls_verify`). |
| `winrm_auth` | string | no | `basic` / `ntlm` | WinRM auth override (default: basic). Some environments require `ntlm`. |
| `rotation_cron` | string | yes | `0 */6 * * *` | Cron schedule in UTC. |

Example (Linux over SSH):

```bash
vault write machine/static-roles/linux_app \
  os="linux" \
  host="<linux-host>" \
  port=22 \
  username="<target-user>" \
  admin_username="<ssh-admin>" \
  admin_private_key=@/path/to/id_rsa \
  rotation_cron="*/5 * * * *"
```

Example (Windows over WinRM):

```bash
vault write machine/static-roles/win_app \
  config_name="win_lab" \
  username="<target-user>" \
  rotation_cron="0 */6 * * *"
```

## Read credentials

Read the current bundle from Vault:

```bash
vault read machine/static-creds/linux_app
```

The response includes:

- `host`, `port`, `username`
- `password`: last password written by Vault (empty until first successful rotation)
- `last_rotated_at`, `next_rotation_at`, `rotation_ttl`

## Security and operational notes

- **Do not commit secrets**: never put real hosts, usernames, or passwords in this repository or in example scripts. Use placeholders.
- **SSH host keys**: v1 uses an insecure host key callback (`InsecureIgnoreHostKey`) to keep PoC friction low. For production, you should pin/verify host keys.
- **WinRM TLS**: prefer HTTPS and valid certificates. `winrm_skip_tls_verify` / `winrm_insecure` is for lab use only.
- **Privilege model**: rotation requires a privileged account. Treat `admin_password` (and optional `sudo_password`) as highly sensitive.

## Windows prerequisites (WinRM)

WinRM authentication and authorization varies by environment. Some hosts reject HTTP Basic or restrict the built-in `Administrator` account for remote management. v1 defaults to `basic`, but you can switch to `winrm_auth=ntlm` when needed.

### Enable the built-in Administrator account (example)

On the Windows machine, open an elevated Command Prompt and run:

```bat
net user administrator /active:yes
```

To set/change the password:

```bat
net user administrator <new-password>
```

### Recommended: create a dedicated local admin (vault-admin)

For testing and operations, prefer a dedicated local admin user (e.g., `vault-admin`) over the built-in `Administrator`.

Example PowerShell (run as Administrator):

```powershell
New-LocalUser -Name "vault-admin" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
Add-LocalGroupMember -Group "Administrators" -Member "vault-admin"
```

### Enable WinRM and allow Basic auth (lab only)

WinRM security is environment-specific. As a *lab* baseline, you typically need:

- WinRM enabled
- Firewall rule allowing inbound WinRM (5985/5986)
- Basic auth enabled (if you intend to use HTTP Basic)
- Unencrypted allowed (only if using HTTP/5985; HTTPS is preferred)

Example PowerShell (run as Administrator):

```powershell
winrm quickconfig -q
winrm set winrm/config/service/auth '@{Basic="true"}'
winrm set winrm/config/service '@{AllowUnencrypted="true"}'
winrm set winrm/config/client/auth '@{Basic="true"}'
winrm set winrm/config/client '@{AllowUnencrypted="true"}'
```

For HTTPS (recommended), configure a listener on 5986 with a valid certificate and keep `AllowUnencrypted` disabled.

### Supported Windows versions

The rotation command uses PowerShell `Set-LocalUser`, which is available on:

- Windows 10 / 11
- Windows Server 2016 / 2019 / 2022

