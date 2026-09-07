// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
)

// Lock dedicates this connection to one operation under a remote kernel flock.
// Releasing or losing the lock closes SSH/SFTP and cancels the operation.
func (c *Client) Lock(ctx context.Context, filename string) (context.Context, func(), error) {
	if err := c.CheckPath(filename); err != nil {
		return nil, nil, err
	}

	lease, cancel := context.WithCancel(ctx)
	closeConnection := sync.OnceFunc(func() {
		_ = c.ssh.Close()
		cancel()
	})
	stop := context.AfterFunc(ctx, closeConnection)
	release := func() {
		stop()
		closeConnection()
	}

	session, err := c.ssh.NewSession()
	if err != nil {
		release()

		return nil, nil, err
	}

	// Keep stdin open so the remote cat holds the lock until connection teardown.
	if _, err := session.StdinPipe(); err != nil {
		release()

		return nil, nil, err
	}

	output, err := session.StdoutPipe()
	if err != nil {
		release()

		return nil, nil, err
	}
	session.Stderr = io.Discard

	command := Command{
		Name: "flock",
		Args: []string{"--exclusive", "--no-fork", filename, "sh", "-c", "printf 'locked\\n'; cat >/dev/null"},
	}
	if err := session.Start(command.shell()); err != nil {
		release()

		return nil, nil, err
	}

	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		release()

		return nil, nil, fmt.Errorf("acquire remote deployment lock: %w", err)
	}
	if line != "locked\n" {
		release()

		return nil, nil, fmt.Errorf("unexpected deployment lock response: %q", line)
	}

	go func() {
		_ = session.Wait()
		release()
	}()

	return lease, release, nil
}
