// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
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
	"golang.org/x/crypto/ssh/agent"
)

func testServer(t *testing.T, options ...sshserver.Option) Config {
	t.Helper()
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
	server.AddHostKey(signer)
	for _, option := range options {
		require.NoError(t, server.SetOption(option))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })

	go func() {
		_ = server.Serve(listener)
	}()

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

func TestSSHFilesAndCommands(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test server emulates a Linux host")
	}
	config := testServer(t)
	client, err := Connect(t.Context(), config)
	require.NoError(t, err)
	defer client.Close()

	file := filepath.Join(t.TempDir(), "nested/config")
	require.NoError(t, client.WriteFile(t.Context(), file, []byte("first"), 0600))
	require.NoError(t, client.WriteFile(t.Context(), file, []byte("second\n"), 0644))
	content, err := client.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "second\n", string(content))

	info, err := client.Stat(file)
	require.NoError(t, err)
	require.EqualValues(t, 0644, info.Mode().Perm())
	require.NoError(t, client.CheckPath(file))
	require.Error(t, client.CheckPath(filepath.Join(file, "child")), "only the final path component may be a regular file")

	value := "apostrophe' ; $(no-command) `no-command`\n"
	output, err := client.Run(t.Context(), Command{Name: "printf", Args: []string{"%s", value}})
	require.NoError(t, err)
	require.Equal(t, value, string(output))

	output, err = client.Run(t.Context(), Command{
		Name: "printenv",
		Args: []string{"HOMELAB_TEST"},
		Env:  []string{"HOMELAB_TEST=" + value},
	})
	require.NoError(t, err)
	require.Equal(t, value+"\n", string(output))

	_, err = client.Run(t.Context(), Command{Name: "sh", Args: []string{"-c", "echo very-secret >&2; exit 1"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "very-secret")

	link := filepath.Join(filepath.Dir(file), "link")
	require.NoError(t, os.Symlink(file, link))
	require.Error(t, client.WriteFile(t.Context(), link, []byte("bad"), 0600))
}

func TestSSHRejectsUntrustedHost(t *testing.T) {
	config := testServer(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(other)
	require.NoError(t, err)
	config.HostKey = string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	_, err = Connect(t.Context(), config)
	require.ErrorContains(t, err, "host key mismatch")
}

func TestSFTPFailureClosesSSH(t *testing.T) {
	connections := make(chan context.Context, 1)
	config := testServer(t, func(server *sshserver.Server) error {
		server.SubsystemHandlers["sftp"] = func(session sshserver.Session) {
			connections <- session.Context()
			_ = session.Exit(1)
		}

		return nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := Connect(ctx, config)
	require.ErrorContains(t, err, "open SFTP")
	require.Nil(t, client)

	connection := <-connections
	select {
	case <-connection.Done():
	case <-time.After(time.Second):
		t.Fatal("failed SFTP initialization left the SSH connection open")
	}
}

func TestSSHAgentReleasedAfterConnect(t *testing.T) {
	config := testServer(t)
	keyBytes, err := os.ReadFile(config.PrivateKeyFile)
	require.NoError(t, err)
	key, err := ssh.ParseRawPrivateKey(keyBytes)
	require.NoError(t, err)
	keyring := agent.NewKeyring()
	require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: key}))

	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("SSH_AUTH_SOCK", socket)

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = agent.ServeAgent(keyring, conn)
	}()

	config.PrivateKeyFile = ""
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := Connect(ctx, config)
	require.NoError(t, err)
	defer client.Close()

	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("agent connection retained after authentication")
	}

	output, err := client.Run(ctx, Command{Name: "printf", Args: []string{"connected"}})
	require.NoError(t, err)
	require.Equal(t, "connected", string(output))
}

func TestInstallStagedFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test server emulates a Linux host")
	}
	client, err := Connect(t.Context(), testServer(t))
	require.NoError(t, err)
	defer client.Close()

	stage := filepath.Join(t.TempDir(), "staged.conf")
	file := filepath.Join(t.TempDir(), "nested/config")
	require.NoError(t, client.WriteFile(t.Context(), file, []byte("old"), 0600))
	require.NoError(t, client.Upload(stage, []byte("validated\n"), 0644))
	require.Error(t, client.Upload(stage, []byte("overwrite"), 0644))
	require.NoError(t, client.InstallFile(t.Context(), stage, file))

	content, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "validated\n", string(content))
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.EqualValues(t, 0644, info.Mode().Perm())

	// A failed copy must preserve the installed file and clean up its temporary file.
	require.Error(t, client.InstallFile(t.Context(), stage+"-missing", file))
	content, err = os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "validated\n", string(content))
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(file), ".homelab-write-*"))
	require.NoError(t, err)
	require.Empty(t, temporary)

	link := filepath.Join(filepath.Dir(file), "link")
	require.NoError(t, os.Symlink(file, link))
	require.Error(t, client.InstallFile(t.Context(), stage, link))
	require.Error(t, client.InstallFile(t.Context(), link, file))
}

func TestRemoteLockCancellationAndReacquire(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("flock is a Linux host prerequisite")
	}
	config := testServer(t)
	client, err := Connect(t.Context(), config)
	require.NoError(t, err)
	defer client.Close()

	filename := filepath.Join(t.TempDir(), "deploy.lock")
	lease, release, err := client.Lock(t.Context(), filename)
	require.NoError(t, err)
	defer release()

	waiter, err := Connect(t.Context(), config)
	require.NoError(t, err)
	defer waiter.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, _, err = waiter.Lock(ctx, filename)
	require.Error(t, err)
	require.NoError(t, lease.Err(), "a waiting deployment must not cancel the lock holder")
	_, err = client.Stat(filename)
	require.NoError(t, err)

	release()
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("lease did not cancel")
	}

	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Second)
	defer cancel2()
	next, err := Connect(ctx2, config)
	require.NoError(t, err)
	defer next.Close()

	_, unlock, err := next.Lock(ctx2, filename)
	require.NoError(t, err)
	unlock()
}

func TestLostLockClosesSFTP(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("flock is a Linux host prerequisite")
	}

	sessions := make(chan sshserver.Session, 1)
	config := testServer(t, func(server *sshserver.Server) error {
		server.Handler = func(session sshserver.Session) {
			sessions <- session
			serveCommand(session)
		}

		return nil
	})
	client, err := Connect(t.Context(), config)
	require.NoError(t, err)
	defer client.Close()

	lease, release, err := client.Lock(t.Context(), filepath.Join(t.TempDir(), "deploy.lock"))
	require.NoError(t, err)
	defer release()

	// Lose only the lock channel: the provider must tear down the other channels.
	lockSession := <-sessions
	require.NoError(t, lockSession.Exit(1))
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("lost lock did not cancel the operation")
	}
	require.NoError(t, t.Context().Err(), "the outer operation context remains alive")

	_, err = client.Stat(config.PrivateKeyFile)
	require.Error(t, err, "SFTP must stop when the lock session ends")
	file := filepath.Join(t.TempDir(), "after-lock-loss")
	require.Error(t, client.Upload(file, []byte("must not be written"), 0600))
	require.NoFileExists(t, file)
}

func TestCommandInput(t *testing.T) {
	client, err := Connect(t.Context(), testServer(t))
	require.NoError(t, err)
	defer client.Close()

	secret := "content\nwith\nnewlines\n"
	result, err := client.Run(t.Context(), Command{Name: "cat", Stdin: strings.NewReader(secret)})
	require.NoError(t, err)
	require.Equal(t, secret, string(result))
	require.NotContains(t, (Command{Name: "cat", Stdin: io.NopCloser(strings.NewReader(secret))}).shell(), secret)
}
