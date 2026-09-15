// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package local accesses a Linux host as the provider process user.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
)

var _ host.Session = (*Client)(nil)

type Client struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	operations sync.WaitGroup
}

func Connect(ctx context.Context) (*Client, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("local transport requires Linux")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	return &Client{ctx: ctx, cancel: cancel}, nil
}

func (c *Client) begin() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ctx.Err(); err != nil {
		return nil, err
	}

	c.operations.Add(1)

	return c.operations.Done, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	c.operations.Wait()
}

func (c *Client) Run(ctx context.Context, command host.Command) ([]byte, error) {
	done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()

	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	configureCommand(cmd)

	cmd.Env = append(os.Environ(), command.Env...)
	cmd.Stdin, cmd.Stderr = command.Stdin, io.Discard

	var stderr host.Stderr
	if command.CaptureStderr {
		cmd.Stderr = &stderr
	}

	cmd.WaitDelay = time.Second
	output, err := cmd.Output()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if err != nil {
		return nil, stderr.Wrap(fmt.Errorf("local %s failed: %w", command.Name, err))
	}

	return output, nil
}

func (c *Client) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()

	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}

	closeOnCancel := context.AfterFunc(c.ctx, func() { _ = conn.Close() })

	return &connection{Conn: conn, stop: closeOnCancel}, nil
}

type connection struct {
	net.Conn
	stop func() bool
}

func (c *connection) Close() error {
	c.stop()
	return c.Conn.Close()
}
