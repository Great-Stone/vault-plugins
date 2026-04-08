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

### Supported Windows versions

The rotation command uses PowerShell `Set-LocalUser`, which is available on:

- Windows 10 / 11
- Windows Server 2016 / 2019 / 2022

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
| `winrm_auth` | string | no | `basic` / `negotiate` / `ntlm` | Windows only. Prefer `negotiate` (treated as NTLM) if Basic is denied by policy. |
| `root_rotation_cron` | string | no | `0 */12 * * *` | Optional: rotate `admin_username` password on schedule (**requires `admin_password`; not supported with `admin_private_key`**). |

Example (Windows profile + dedicated admin):

```bash
vault write machine/config/win_lab \
  name=win_lab \
  os=windows \
  host="<windows-host>" \
  port=5985 \
  admin_username="vault-admin" \
  admin_password="<vault-admin-password>" \
  winrm_auth=negotiate
```

### Static roles: `machine/static-roles/<name>`

A static role represents **one local OS account**, and it **must** reference a connection profile (`config/<name>`):

- **username**: the local account whose password Vault stores/rotates
- **config/<name>**: provides host/transport + privileged credential used to perform the password change
- **rotation_cron**: UTC cron schedule for rotation

The scheduler checks due roles periodically and rotates when due. Rotation timing is independent from Vault lease TTL returned by `static-creds` reads.

#### Static role fields

| Field | Type | Required | Example | Notes |
|---|---:|:---:|---|---|
| `config_name` | string | **yes** | `win_lab` | Inherits host/transport/admin settings from `config/<name>`. |
| `username` | string | **yes** | `appuser` | Managed account (password returned on `static-creds`). |
| `validate_user_exists` | bool | no | `true` | If true, validate that `username` exists on the host during write (uses the referenced config profile admin credential). |
| `password` | string | no | `...` | Optional initial value; otherwise set after first successful rotation. |
| `rotation_cron` | string | **yes** | `0 */6 * * *` | Cron schedule in UTC. |

Example (create a test target user on Linux):

```bash
sudo useradd -m "<target-user>" || true
sudo passwd "<target-user>"
```

Example (Linux over SSH via config profile):

```bash
vault write machine/config/linux_lab \
  name=linux_lab \
  os=linux \
  host="<linux-host>" \
  port=22 \
  admin_username="<ssh-admin>" \
  admin_private_key=@/path/to/id_rsa

vault write machine/static-roles/linux_app \
  config_name="linux_lab" \
  username="<target-user>" \
  validate_user_exists=true \
  rotation_cron="*/5 * * * *"
```

Example (create a test target user on Windows):

```powershell
New-LocalUser -Name "<target-user>" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
```

Example (Windows over WinRM via config profile):

```bash
vault write machine/static-roles/win_app \
  config_name="win_lab" \
  username="<target-user>" \
  validate_user_exists=true \
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
- **WinRM TLS**: prefer HTTPS and valid certificates. `winrm_skip_tls_verify` is for lab use only.
- **Privilege model**: rotation requires a privileged account. Treat `admin_password` (and optional `sudo_password`) as highly sensitive.

## Linux: SSH password login (optional)

On Linux, this plugin rotates the **local password** using `chpasswd`. However, many distros/cloud images disable SSH password auth by default (key-only). In that case, the password may rotate correctly but `ssh user@host` password login will still fail.

### Troubleshooting: PAM disables password auth

If you see symptoms like:

- even when forcing password auth (`ssh -o PreferredAuthentications=password ...`), the server still only offers `publickey` / `keyboard-interactive` (and never `password`)

then this is often caused by **PAM configuration** (e.g., `/etc/pam.d/sshd`) rather than `sshd_config` alone. This is common when a host has been customized for flows like “Vault SSH helper”, where the `password-auth` include is commented out or the PAM stack has been altered.

Check:

```bash
sudo grep -nE 'password-auth|pam_unix|vault-ssh-helper' /etc/pam.d/sshd
```

If `password-auth` includes are commented out, normal SSH password login will not work unless you revert those changes.

### Allow password auth only for `vault-machine-test`

You can scope SSH password auth to a single user using `Match User`.

Example (as root):

```bash
sudo mkdir -p /etc/ssh/sshd_config.d

sudo sh -c 'cat >/etc/ssh/sshd_config.d/99-vault-machine-test.conf <<'"'"'EOF'"'"'
Match User vault-machine-test
  PasswordAuthentication yes
  KbdInteractiveAuthentication yes
  UsePAM yes
EOF'

# Some environments do not include sshd_config.d by default.
# If nothing is printed, add this line to /etc/ssh/sshd_config:
#   Include /etc/ssh/sshd_config.d/*.conf
sudo grep -nE '^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config || true

sudo sshd -t
sudo systemctl restart sshd
```

To test from a client while forcing password auth:

```bash
# Note: use your system `ssh` command (not Vault's `vault ssh`).
ssh -o PreferredAuthentications=password -o PubkeyAuthentication=no vault-machine-test@<linux-host>
```

## Windows prerequisites (WinRM)

### Create a dedicated local admin (vault-admin)

For testing and operations, prefer a dedicated local admin user (e.g., `vault-admin`) over the built-in `Administrator`.

Example PowerShell (run as Administrator):

```powershell
New-LocalUser -Name "vault-admin" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
Add-LocalGroupMember -Group "Administrators" -Member "vault-admin"
```

### Create the target account for static roles (e.g., `vault-machine-test`)

The `username` you set on `machine/static-roles/<name>` must already exist as a **local user** on the Windows host.

Example PowerShell (run as Administrator):

```powershell
# Create the target local user (example: vault-machine-test)
New-LocalUser -Name "vault-machine-test" -Password (ConvertTo-SecureString "<INITIAL_PASSWORD>" -AsPlainText -Force)
```

If you also want to validate the rotated password by logging in over RDP (optional), grant remote sign-in rights. The simplest approach is adding the user to the local `Remote Desktop Users` group:

```powershell
Add-LocalGroupMember -Group "Remote Desktop Users" -Member "vault-machine-test"
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

#### (Important) `Access is denied` can be caused by account lockout

If you repeatedly try WinRM/PowerShell remoting with wrong credentials, the local admin account (e.g., `vault-admin`) can become **locked out**. In that state, authentication failures often show up as `Access is denied` (`0x80070005`), which can look like a WinRM configuration issue.

- Check lockout state: `net user vault-admin` (look for `Account active` showing `Locked`)
- Check lockout policy: `net accounts`
- For lab/PoC, ensure the local account **has a password set** (if `Password required` is `No`, it may be an unintended configuration)

For HTTPS (recommended), configure a listener on 5986 with a valid certificate and keep `AllowUnencrypted` disabled.

