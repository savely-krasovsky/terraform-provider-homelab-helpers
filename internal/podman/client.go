// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package podman talks to the rootless Podman service over its REST socket.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
)

type Client struct {
	http     *http.Client
	endpoint string
	cancel   context.CancelFunc
}

const (
	OwnerLabel   = "io.terraform.quadlet.owner"
	VersionLabel = "io.terraform.quadlet.version"
)

// Secret contains metadata only. Inspect never requests the secret value.
type Secret struct {
	ID   string `json:"ID"`
	Spec struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	} `json:"Spec"`
}

// New accepts an HTTP client and API origin independently of host access.
func New(client *http.Client, endpoint string) *Client {
	return &Client{http: client, endpoint: strings.TrimRight(endpoint, "/")}
}

// Connect reaches an explicit Unix socket through the supplied dialer.
func Connect(ctx context.Context, socket string, dial host.Dialer) *Client {
	lifetime, cancel := context.WithCancel(ctx)
	client := New(&http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// net/http may detach an in-progress dial from its request to reuse
			// the connection. It must still end with this provider operation.
			ctx, stopDial := context.WithCancel(ctx)
			defer stopDial()

			stop := context.AfterFunc(lifetime, stopDial)
			defer stop()

			return dial(ctx, "unix", socket)
		},
	}}, "http://podman")
	client.cancel = cancel

	return client
}

func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}

	c.http.CloseIdleConnections()
}

func (c *Client) Inspect(ctx context.Context, name string) (*Secret, error) {
	status, body, err := c.call(ctx, http.MethodGet, "/secrets/"+url.PathEscape(name)+"/json", "")
	if err != nil {
		return nil, err
	}

	switch status {
	case http.StatusOK:
		var secret Secret
		if err := json.Unmarshal([]byte(body), &secret); err != nil || secret.ID == "" || secret.Spec.Name == "" {
			return nil, fmt.Errorf("inspect secret %s: invalid metadata", name)
		}

		// Podman accepts partial names and IDs. A resource owns an exact name.
		if secret.Spec.Name != name {
			return nil, nil
		}

		return &secret, nil

	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("inspect secret %s: %s", name, body)
	}
}

// Create never replaces an existing name, including a concurrent creator.
func (c *Client) Create(ctx context.Context, name, owner, version, value string) (string, error) {
	labels, _ := json.Marshal(map[string]string{OwnerLabel: owner, VersionLabel: version})
	query := url.Values{"name": {name}, "labels": {string(labels)}}

	status, body, err := c.call(ctx, http.MethodPost, "/secrets/create?"+query.Encode(), value)
	if err != nil {
		return "", err
	}

	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("create secret %s: %s", name, body)
	}

	var result struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil || result.ID == "" {
		return "", fmt.Errorf("create secret %s: missing ID in response", name)
	}

	return result.ID, nil
}

// Remove addresses the inspected object by ID, never a reusable name.
func (c *Client) Remove(ctx context.Context, id string) error {
	status, body, err := c.call(ctx, http.MethodDelete, "/secrets/"+url.PathEscape(id), "")
	if err != nil {
		return err
	}

	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("remove secret %s: %s", id, body)
	}

	return nil
}

func (c *Client) call(ctx context.Context, method, path, body string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+"/v5.0.0/libpod"+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}

	response, err := c.http.Do(request)
	if err != nil {
		// Closing the operation's dial may race the request deadline. Preserve
		// the request's cause instead of reporting the dial's generic cancellation.
		if ctx.Err() != nil {
			err = ctx.Err()
		}

		return 0, "", fmt.Errorf("podman API: %w", err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return 0, "", err
	}

	return response.StatusCode, strings.TrimSpace(string(content)), nil
}
