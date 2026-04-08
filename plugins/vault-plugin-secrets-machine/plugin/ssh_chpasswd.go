package plugin

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func rotateLinuxPassword(host string, port int, adminUser, adminPass, adminPrivateKeyPEM, adminPrivateKeyPassphrase, sudoPass, targetUser, newPassword string) error {
	if strings.TrimSpace(adminUser) == "" {
		return fmt.Errorf("admin username is empty")
	}
	if strings.TrimSpace(targetUser) == "" {
		return fmt.Errorf("target username is empty")
	}

	line := fmt.Sprintf("%s:%s\n", targetUser, newPassword)
	lineB64 := base64.StdEncoding.EncodeToString([]byte(line))

	var remoteCmd string
	switch {
	case strings.TrimSpace(adminUser) == "root" && strings.TrimSpace(sudoPass) == "":
		remoteCmd = fmt.Sprintf(`set -e; bash -c 'echo %s | base64 -d | chpasswd'`, lineB64)
	case strings.TrimSpace(sudoPass) != "":
		sudoB64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(sudoPass)))
		remoteCmd = fmt.Sprintf(
			`set -e; echo %s | base64 -d | sudo -S bash -c 'echo %s | base64 -d | chpasswd'`,
			sudoB64, lineB64,
		)
	default:
		remoteCmd = fmt.Sprintf(
			`set -e; sudo -n bash -c 'echo %s | base64 -d | chpasswd'`,
			lineB64,
		)
	}

	authMethod, err := buildSSHAuthMethod(adminPass, adminPrivateKeyPEM, adminPrivateKeyPassphrase)
	if err != nil {
		return err
	}

	config := &ssh.ClientConfig{
		User:            adminUser,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Run(remoteCmd); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("remote chpasswd: %w: %s", err, msg)
		}
		return fmt.Errorf("remote chpasswd: %w", err)
	}
	return nil
}

func buildSSHAuthMethod(adminPass, adminPrivateKeyPEM, adminPrivateKeyPassphrase string) (ssh.AuthMethod, error) {
	if strings.TrimSpace(adminPrivateKeyPEM) != "" {
		keyBytes := []byte(adminPrivateKeyPEM)
		var signer ssh.Signer
		var err error
		if strings.TrimSpace(adminPrivateKeyPassphrase) != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(adminPrivateKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyBytes)
		}
		if err != nil {
			return nil, fmt.Errorf("parse ssh private key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}

	if strings.TrimSpace(adminPass) == "" {
		return nil, fmt.Errorf("admin_password or admin_private_key is required for ssh auth")
	}
	return ssh.Password(adminPass), nil
}