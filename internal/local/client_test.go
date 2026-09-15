// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
)

func session(t *testing.T) *Client {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("local transport requires Linux")
	}

	c, err := Connect(t.Context())
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return c
}

func TestLocalFileReplacementAndPaths(t *testing.T) {
	c := session(t)
	root := t.TempDir()
	name := filepath.Join(root, "config", "app.conf")
	require.NoError(t, c.WriteFile(t.Context(), name, []byte("first"), 0600))

	old, err := os.Open(name)
	require.NoError(t, err)

	defer old.Close()

	require.NoError(t, c.WriteFile(t.Context(), name, []byte("second\n"), 0644))

	content, err := c.ReadFile(name)
	require.NoError(t, err)
	require.Equal(t, "second\n", string(content))

	buffer := make([]byte, 5)
	_, err = old.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, "first", string(buffer), "replacement must preserve the old inode for existing readers")

	info, err := c.Stat(name)
	require.NoError(t, err)
	require.EqualValues(t, 0644, info.Mode().Perm())
	require.Error(t, c.Upload(name, []byte("overwrite"), 0600))
	require.Error(t, c.CheckPath(filepath.Join(name, "child")))

	_, err = c.ReadFile(filepath.Dir(name))
	require.Error(t, err)

	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(name, link))
	require.Error(t, c.WriteFile(t.Context(), link, []byte("bad"), 0600))
	require.Error(t, c.Remove(link))
	require.Error(t, c.CheckPath("relative"))
	require.Error(t, c.RemoveStage(root))

	stage := filepath.Join(root, ".terraform-stage-test")
	source := filepath.Join(stage, "app.conf")
	require.NoError(t, c.Upload(source, []byte("installed"), 0600))
	require.NoError(t, c.InstallFile(t.Context(), source, name))

	content, err = c.ReadFile(name)
	require.NoError(t, err)
	require.Equal(t, "installed", string(content))
	require.NoError(t, c.RemoveStage(stage))
	require.NoDirExists(t, stage)

	entries, err := c.ReadDir(filepath.Dir(name))
	require.NoError(t, err)
	require.Equal(t, []string{"app.conf"}, entries)
	require.NoError(t, c.Remove(name))
	require.NoError(t, c.Remove(name))
}

func TestLocalCommandsAndCancellation(t *testing.T) {
	c := session(t)
	value := "apostrophe' ; $(no-command) `no-command`\n"
	output, err := c.Run(t.Context(), host.Command{Name: "printf", Args: []string{"%s", value}})
	require.NoError(t, err)
	require.Equal(t, value, string(output))

	output, err = c.Run(t.Context(), host.Command{Name: "sh", Args: []string{"-c", `printf %s "$TEST_COMMAND_VALUE"`}, Env: []string{"TEST_COMMAND_VALUE=" + value}})
	require.NoError(t, err)
	require.Equal(t, value, string(output))

	_, err = c.Run(t.Context(), host.Command{Name: "sh", Args: []string{"-c", "echo sensitive >&2; exit 1"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive")

	_, err = c.Run(t.Context(), host.Command{
		Name: "sh", Args: []string{"-c", "echo app.container: unsupported-key >&2; exit 1"}, CaptureStderr: true,
	})
	require.ErrorContains(t, err, "app.container: unsupported-key")

	ctx, cancel := context.WithCancel(t.Context())
	root := t.TempDir()
	ready, escaped := filepath.Join(root, "ready"), filepath.Join(root, "escaped")
	result := make(chan error, 1)

	go func() {
		_, err := c.Run(ctx, host.Command{Name: "sh", Args: []string{"-c", `touch "$1"; (sleep 0.3; touch "$2") & wait`, "test", ready, escaped}})
		result <- err
	}()

	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 5*time.Millisecond)
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("command was not cancelled")
	}

	<-time.After(400 * time.Millisecond)
	require.NoFileExists(t, escaped, "cancelled child must not outlive its host operation")
	c.Close()
	require.ErrorIs(t, c.Upload(filepath.Join(root, "after-close"), nil, 0600), context.Canceled)
}

func TestLocalLockCancellationAndReacquisition(t *testing.T) {
	first, second := session(t), session(t)
	name := filepath.Join(t.TempDir(), "config.lock")
	lease, unlock, err := first.Lock(t.Context(), name)
	require.NoError(t, err)

	defer unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()

	_, _, err = second.Lock(ctx, name)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	first.Close()

	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("closing local session did not cancel lease")
	}

	third := session(t)
	_, release, err := third.Lock(t.Context(), name)
	require.NoError(t, err)
	release()
	require.ErrorIs(t, third.CheckPath(name), context.Canceled)
}
