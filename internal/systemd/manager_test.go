// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package systemd

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The local user bus stands in for the forwarded one; it is only read.
func TestManagerOverAUserBus(t *testing.T) {
	uid := os.Getuid()

	bus := fmt.Sprintf("/run/user/%d/bus", uid)
	if _, err := os.Stat(bus); err != nil {
		t.Skip("no user bus to talk to")
	}

	manager, err := Connect(t.Context(), uid, bus, (&net.Dialer{}).DialContext)
	require.NoError(t, err)

	defer manager.Close()

	loaded, err := manager.Loaded(t.Context(), []string{"basic.target", "provider-test-missing.service"})
	require.NoError(t, err)
	require.Equal(t, []string{"basic.target"}, loaded)
	require.NoError(t, manager.Stop(t.Context(), nil))
}

func TestBusAuthenticationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := Connect(ctx, 1000, "/custom/bus", func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "unix", network)
		require.Equal(t, "/custom/bus", address)

		client, server := net.Pipe()

		t.Cleanup(func() { _ = server.Close() })

		return client, nil
	})
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
