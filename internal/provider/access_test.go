// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
)

// Unimplemented capabilities panic: a secret operation must never touch the
// deployment filesystem, lock, generator or systemd discovery.
type limitedSession struct {
	host.Session
	run    func(context.Context, host.Command) ([]byte, error)
	dial   host.Dialer
	closed chan struct{}
	once   sync.Once
}

func (s *limitedSession) Run(ctx context.Context, c host.Command) ([]byte, error) {
	return s.run(ctx, c)
}
func (s *limitedSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return s.dial(ctx, network, address)
}
func (s *limitedSession) Close() { s.once.Do(func() { close(s.closed) }) }

func TestPodmanAccessNeedsOnlyItsSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ID":"podman-id","Spec":{"Name":"app"}}`))
	}))
	defer server.Close()

	var sessions []*limitedSession

	a := &access{timeout: time.Second, podmanSocket: "/custom/podman.sock", open: func(context.Context) (host.Session, error) {
		s := &limitedSession{closed: make(chan struct{}), dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			require.Equal(t, "unix", network)
			require.Equal(t, "/custom/podman.sock", address)

			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		}}
		sessions = append(sessions, s)

		return s, nil
	}}
	for range 2 {
		require.NoError(t, a.secrets(t.Context(), func(ctx context.Context, client secretClient) error {
			found, err := client.Inspect(ctx, "app")
			require.NotNil(t, found)

			return err
		}))
	}

	require.Len(t, sessions, 2)

	for _, s := range sessions {
		select {
		case <-s.closed:
		default:
			t.Fatal("operation leaked a host session")
		}
	}
}

func TestOperationTimeoutClosesTheSession(t *testing.T) {
	s := &limitedSession{closed: make(chan struct{})}
	a := &access{timeout: 25 * time.Millisecond, open: func(context.Context) (host.Session, error) { return s, nil }}
	err := a.withHost(t.Context(), func(ctx context.Context, _ host.Session) error {
		select {
		case <-s.closed:
			return ctx.Err()
		case <-time.After(time.Second):
			t.Fatal("operation timeout did not close session")
			return nil
		}
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
