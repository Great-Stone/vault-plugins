package plugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/masterzen/winrm"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func rotateWindowsPassword(ctx context.Context, authType string, host string, port int, useHTTPS, insecureTLS bool, adminUser, adminPass, targetUser, newPassword string) error {
	if strings.TrimSpace(adminUser) == "" {
		return fmt.Errorf("admin username is empty")
	}
	if strings.TrimSpace(targetUser) == "" {
		return fmt.Errorf("target username is empty")
	}

	ub64 := base64.StdEncoding.EncodeToString([]byte(targetUser))
	pb64 := base64.StdEncoding.EncodeToString([]byte(newPassword))
	script := fmt.Sprintf(
		`$u = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); $p = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); $sec = ConvertTo-SecureString $p -AsPlainText -Force; Set-LocalUser -Name $u -Password $sec`,
		ub64, pb64,
	)

	enc, err := powershellEncodedCommand(script)
	if err != nil {
		return err
	}
	cmd := `powershell.exe -NonInteractive -NoProfile -ExecutionPolicy Bypass -EncodedCommand ` + enc

	endpoint := winrm.NewEndpoint(host, port, useHTTPS, insecureTLS, nil, nil, nil, 90*time.Second)
	params := *winrm.DefaultParameters
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "", "basic":
		// default transport uses HTTP Basic auth
	case "ntlm":
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	default:
		return fmt.Errorf("unsupported winrm auth type %q", authType)
	}
	client, err := winrm.NewClientWithParameters(endpoint, adminUser, adminPass, &params)
	if err != nil {
		return fmt.Errorf("winrm client: %w", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode, err := client.RunWithContext(ctx, cmd, &stdout, &stderr)
	if err != nil {
		return fmt.Errorf("winrm run: %w", err)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return fmt.Errorf("winrm exit %d: %s", exitCode, msg)
		}
		return fmt.Errorf("winrm exit %d", exitCode)
	}
	return nil
}

func powershellEncodedCommand(script string) (string, error) {
	enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	out, _, err := transform.Bytes(enc, []byte(script))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}
