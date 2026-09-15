// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	sshserver "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
)

func testServer(t *testing.T) Config {
	t.Helper()
	return startTestServer(t, true)
}

func startTestServer(t *testing.T, withSFTP bool) Config {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("the test server emulates a Linux host")
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)

	key, err := ssh.MarshalPrivateKey(private, "test")
	require.NoError(t, err)

	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(key), 0600))

	server := &sshserver.Server{
		Handler: serveCommand,
		PublicKeyHandler: func(_ sshserver.Context, key sshserver.PublicKey) bool {
			return sshserver.KeysEqual(key, signer.PublicKey())
		},
		SubsystemHandlers: map[string]sshserver.SubsystemHandler{"sftp": serveSFTP},
	}
	if !withSFTP {
		server.SubsystemHandlers = nil
	}

	server.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })

	go func() { _ = server.Serve(listener) }()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	portNumber, err := strconv.Atoi(port)
	require.NoError(t, err)

	return Config{
		Host: host, Port: portNumber, User: "test",
		PrivateKeyFile: keyFile,
		HostKey:        strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
	}
}

func serveSFTP(session sshserver.Session) {
	server, err := sftp.NewServer(session)
	if err != nil {
		_ = session.Exit(1)

		return
	}
	defer server.Close()

	_ = server.Serve()
}

func serveCommand(session sshserver.Session) {
	cmd := exec.CommandContext(session.Context(), "sh", "-c", session.RawCommand())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = session, session, session.Stderr()

	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		_ = session.Exit(1)
	}
}

func TestFilesCommandsAndUID(t *testing.T) {
	config := testServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	client, err := Connect(ctx, config)

	cancel()
	require.NoError(t, err)

	defer client.Close()

	file := filepath.Join(t.TempDir(), "nested/config")
	require.NoError(t, client.WriteFile(t.Context(), file, []byte("first"), 0600))
	require.NoError(t, client.WriteFile(t.Context(), file, []byte("second\n"), 0644))

	content, err := client.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "second\n", string(content))
	require.Error(t, client.CheckPath(filepath.Join(file, "child")))

	value := "apostrophe' ; $(no-command) `no-command`\n"
	output, err := client.Run(t.Context(), host.Command{Name: "printf", Args: []string{"%s", value}})
	require.NoError(t, err)
	require.Equal(t, value, string(output))

	_, err = client.Run(t.Context(), host.Command{Name: "sh", Args: []string{"-c", "echo very-secret >&2; exit 1"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "very-secret")

	_, err = client.Run(t.Context(), host.Command{
		Name: "sh", Args: []string{"-c", "echo app.container: unsupported-key >&2; exit 1"}, CaptureStderr: true,
	})
	require.ErrorContains(t, err, "app.container: unsupported-key")

	uid, err := host.UserID(t.Context(), client)
	require.NoError(t, err)
	require.Equal(t, os.Getuid(), uid)

	link := filepath.Join(filepath.Dir(file), "link")
	require.NoError(t, os.Symlink(file, link))

	info, err := client.Stat(link)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())

	_, err = client.ReadFile(link)
	require.Error(t, err)
	require.Error(t, client.WriteFile(t.Context(), link, []byte("bad"), 0600))
}

func TestConnectionOutlivesItsContext(t *testing.T) {
	config := testServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	client, err := Connect(ctx, config)
	require.NoError(t, err)

	defer client.Close()

	cancel()

	_, err = client.Run(t.Context(), host.Command{Name: "true"})
	require.NoError(t, err)
}

func TestUntrustedHostIsRejected(t *testing.T) {
	config := testServer(t)
	config.HostKey = ""
	config.KnownHostsFile = filepath.Join(t.TempDir(), "known_hosts")
	require.NoError(t, os.WriteFile(config.KnownHostsFile, nil, 0600))

	_, err := Connect(t.Context(), config)
	require.Error(t, err)
}

func TestCommandsDoNotRequireSFTP(t *testing.T) {
	client, err := Connect(t.Context(), startTestServer(t, false))
	require.NoError(t, err)

	defer client.Close()

	_, err = client.Run(t.Context(), host.Command{Name: "true"})
	require.NoError(t, err)

	_, err = client.ReadFile(filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "open SFTP")
}
