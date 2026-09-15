// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package podman

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSecretsOverTheSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)

	secrets := map[string]string{}
	labels := map[string]string{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v5.0.0/libpod/secrets/create":
			body, _ := io.ReadAll(r.Body)

			name := r.URL.Query().Get("name")
			if _, exists := secrets[name]; exists && r.URL.Query().Get("replace") != "true" {
				http.Error(w, `{"message":"already exists"}`, http.StatusConflict)

				return
			}

			require.Empty(t, r.URL.Query().Get("replace"))
			require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("labels")), &labels))

			secrets[name] = string(body)

			w.WriteHeader(http.StatusCreated)

			_, _ = w.Write([]byte(`{"ID":"native-id"}`))

		case r.Method == http.MethodGet && r.URL.Path == "/v5.0.0/libpod/secrets/app/json":
			if _, exists := secrets["app"]; exists {
				_ = json.NewEncoder(w).Encode(map[string]any{"ID": "native-id", "Spec": map[string]any{"Name": "app", "Labels": labels}})
			} else {
				w.WriteHeader(http.StatusNotFound)
			}

		case r.Method == http.MethodDelete && r.URL.Path == "/v5.0.0/libpod/secrets/native-id":
			delete(secrets, "app")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	server.Listener = listener

	server.Start()
	defer server.Close()

	client := Connect(t.Context(), socket, (&net.Dialer{}).DialContext)
	defer client.Close()

	found, err := client.Inspect(t.Context(), "app")
	require.NoError(t, err)
	require.Nil(t, found)

	id, err := client.Create(t.Context(), "app", "owner", "1", "first\n")
	require.NoError(t, err)
	require.Equal(t, "native-id", id)

	_, err = client.Create(t.Context(), "app", "another-owner", "1", "second\n")
	require.Error(t, err)
	require.Equal(t, "first\n", secrets["app"])
	require.Equal(t, "owner", labels[OwnerLabel])

	found, err = client.Inspect(t.Context(), "app")
	require.NoError(t, err)
	require.NotNil(t, found)

	require.NoError(t, client.Remove(t.Context(), "native-id"))
	require.NoError(t, client.Remove(t.Context(), "native-id"))
	require.Empty(t, secrets)
}

func TestSocketDialHonorsRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	started := make(chan struct{})

	client := Connect(ctx, "/custom/podman.sock", func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "unix", network)
		require.Equal(t, "/custom/podman.sock", address)
		close(started)
		<-ctx.Done()

		return nil, ctx.Err()
	})
	defer client.Close()

	_, err := client.Inspect(ctx, "app")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	<-started
}

func TestHTTPClientHasNoHostDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v5.0.0/libpod/secrets/app/json", r.URL.Path)

		_, _ = w.Write([]byte(`{"ID":"native-id","Spec":{"Name":"app"}}`))
	}))
	defer server.Close()

	client := New(server.Client(), server.URL)
	defer client.Close()

	found, err := client.Inspect(t.Context(), "app")
	require.NoError(t, err)
	require.NotNil(t, found)
}
