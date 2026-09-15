// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/deployment"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/podman"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/quadlet"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/systemd"
)

// access composes feature adapters over one session per resource operation.
// No connection or lock is shared with another resource.
type access struct {
	open         host.Open
	timeout      time.Duration
	podmanSocket string
	systemdBus   string
}

type secretClient interface {
	Create(context.Context, string, string, string, string) (string, error)
	Inspect(context.Context, string) (*podman.Secret, error)
	Remove(context.Context, string) error
}

type secretAccess interface {
	secrets(context.Context, func(context.Context, secretClient) error) error
}

func (a *access) withHost(ctx context.Context, fn func(context.Context, host.Session) error) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	client, err := a.open(ctx)
	if err != nil {
		return err
	}

	stop := context.AfterFunc(ctx, client.Close)
	defer func() { stop(); client.Close() }()

	return fn(ctx, client)
}

func (a *access) secrets(ctx context.Context, fn func(context.Context, secretClient) error) error {
	return a.withHost(ctx, func(ctx context.Context, session host.Session) error {
		socket := a.podmanSocket
		if socket == "" {
			uid, err := host.UserID(ctx, session)
			if err != nil {
				return err
			}

			socket = fmt.Sprintf("/run/user/%d/podman/podman.sock", uid)
		}

		client := podman.Connect(ctx, socket, session.DialContext)
		defer client.Close()

		return fn(ctx, client)
	})
}

// engine serializes deployments of this host user, including refresh. The host
// lock and cancellation contract are identical for SSH and local sessions.
func (a *access) engine(ctx context.Context, manageUnits bool, fn func(context.Context, deployment.Engine) error) error {
	return a.withHost(ctx, func(ctx context.Context, session host.Session) error {
		dirs, err := systemd.UserDirectories(ctx, session)
		if err != nil {
			return err
		}

		paths := deployment.Paths{Config: dirs.Config, State: path.Join(dirs.State, "terraform-quadlet")}

		engine := deployment.Engine{Host: session, Paths: paths, Quadlets: quadlet.Validator{Host: session}}
		if manageUnits {
			if err := deployment.Prepare(ctx, session, paths); err != nil {
				return err
			}
		} else if _, err := session.Stat(paths.State); errors.Is(err, fs.ErrNotExist) {
			return fn(ctx, engine)
		} else if err != nil {
			return err
		}

		ctx, release, err := session.Lock(ctx, path.Join(paths.State, "config.lock"))
		if err != nil {
			return err
		}
		defer release()

		if manageUnits {
			uid, err := host.UserID(ctx, session)
			if err != nil {
				return err
			}

			bus := a.systemdBus
			if bus == "" {
				bus = fmt.Sprintf("/run/user/%d/bus", uid)
			}

			manager, err := systemd.Connect(ctx, uid, bus, session.DialContext)
			if err != nil {
				return err
			}
			defer manager.Close()

			engine.Units = manager
		}

		return fn(ctx, engine)
	})
}
