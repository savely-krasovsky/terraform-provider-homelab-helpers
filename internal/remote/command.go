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

	"al.essio.dev/pkg/shellescape"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
)

// Run executes the command through the remote login shell; secret values travel through stdin or SFTP.
func (c *Client) Run(ctx context.Context, command host.Command) ([]byte, error) {
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

	var stderr host.Stderr
	if command.CaptureStderr {
		session.Stderr = &stderr
	}

	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()

	if err := session.Run(shell(command)); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, stderr.Wrap(fmt.Errorf("remote %s failed: %w", command.Name, err))
	}

	return stdout.Bytes(), nil
}

func shell(command host.Command) string {
	words := slices.Concat([]string{command.Name}, command.Args)
	if len(command.Env) > 0 {
		words = slices.Concat([]string{"env"}, command.Env, words)
	}

	for i := range words {
		words[i] = shellescape.Quote(words[i])
	}

	return strings.Join(words, " ")
}
