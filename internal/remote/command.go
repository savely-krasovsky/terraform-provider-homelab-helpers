// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

type Command struct {
	Name  string
	Args  []string
	Env   []string
	Stdin io.Reader
}

// shell quotes every argument for POSIX shells. Secret values travel through stdin or SFTP.
func (c Command) shell() string {
	args := slices.Concat([]string{c.Name}, c.Args)
	if len(c.Env) > 0 {
		args = slices.Concat([]string{"env"}, c.Env, args)
	}

	for i := range args {
		args[i] = "'" + strings.ReplaceAll(args[i], "'", "'\"'\"'") + "'"
	}

	return strings.Join(args, " ")
}

func (c *Client) Run(ctx context.Context, command Command) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	session, err := c.ssh.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open SSH command: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = io.Discard
	session.Stdin = command.Stdin

	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()

	if err := session.Run(command.shell()); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("remote %s failed: %w", command.Name, err)
	}

	return stdout.Bytes(), nil
}
