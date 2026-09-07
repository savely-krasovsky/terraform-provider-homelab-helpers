// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Config struct {
	Host           string
	Port           int
	User           string
	PrivateKeyFile string
	KnownHostsFile string
	HostKey        string
}

type Client struct {
	ssh  *ssh.Client
	fs   *sftp.Client
	stop func() bool
}

func expandHome(name string) (string, error) {
	if name != "~" && !strings.HasPrefix(name, "~/") {
		return name, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if name == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(name, "~/")), nil
}

func Connect(ctx context.Context, config Config) (*Client, error) {
	address := net.JoinHostPort(config.Host, fmt.Sprint(config.Port))
	verify, algorithms, err := hostVerifier(config, address)
	if err != nil {
		return nil, err
	}

	auth, agentConn, err := authentication(ctx, config.PrivateKeyFile)
	if err != nil {
		return nil, err
	}

	// The agent is only used during authentication, not by established sessions.
	if agentConn != nil {
		defer agentConn.Close()
	}

	sshConfig := &ssh.ClientConfig{
		User:              config.User,
		Auth:              auth,
		HostKeyCallback:   verify,
		HostKeyAlgorithms: algorithms,
	}

	// A newly booted VM may not have started sshd yet. Authentication and host
	// verification failures are permanent; only connection failures are retried.
	var conn net.Conn
	for {
		conn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			break
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect to SSH: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}

	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	connection, channels, requests, err := ssh.NewClientConn(conn, address, sshConfig)
	if err != nil {
		stop()

		return nil, fmt.Errorf("SSH handshake: %w", err)
	}

	_ = conn.SetDeadline(time.Time{})
	sshClient := ssh.NewClient(connection, channels, requests)

	files, err := sftp.NewClient(sshClient)
	if err != nil {
		stop()
		_ = sshClient.Close()

		return nil, fmt.Errorf("open SFTP: %w", err)
	}

	return &Client{ssh: sshClient, fs: files, stop: stop}, nil
}

func (c *Client) Close() {
	c.stop()
	_ = c.fs.Close()
	_ = c.ssh.Close()
}

func hostVerifier(config Config, address string) (ssh.HostKeyCallback, []string, error) {
	if config.HostKey != "" {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(config.HostKey))
		if err != nil {
			return nil, nil, fmt.Errorf("parse SSH host_key: %w", err)
		}

		algorithms := []string{key.Type()}
		if key.Type() == ssh.KeyAlgoRSA {
			algorithms = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
		}

		return ssh.FixedHostKey(key), algorithms, nil
	}

	filename, err := expandHome(config.KnownHostsFile)
	if err != nil {
		return nil, nil, err
	}

	db, err := knownhosts.NewDB(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("read known_hosts (or configure a trusted host_key): %w", err)
	}

	return db.HostKeyCallback(), db.HostKeyAlgorithms(address), nil
}

func authentication(ctx context.Context, filename string) ([]ssh.AuthMethod, net.Conn, error) {
	if filename != "" {
		filename, err := expandHome(filename)
		if err != nil {
			return nil, nil, err
		}

		pem, err := os.ReadFile(filename)
		if err != nil {
			return nil, nil, fmt.Errorf("read SSH private key: %w", err)
		}
		defer clear(pem)

		key, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, nil, errors.New("cannot parse SSH private key; load encrypted keys into ssh-agent and omit private_key_file")
		}

		return []ssh.AuthMethod{ssh.PublicKeys(key)}, nil, nil
	}

	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, nil, errors.New("set private_key_file or load a key into SSH_AUTH_SOCK")
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to ssh-agent: %w", err)
	}

	return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, conn, nil
}
