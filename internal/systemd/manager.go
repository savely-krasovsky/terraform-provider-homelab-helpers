// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package systemd drives the user service manager over its D-Bus socket.
package systemd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
)

type Manager struct {
	conn         *dbus.Conn
	closeSockets []func()
}

// Connect authenticates as uid to an explicit user bus through dial.
func Connect(ctx context.Context, uid int, bus string, dial host.Dialer) (*Manager, error) {
	var closeSockets []func()

	conn, err := dbus.NewConnection(func() (*godbus.Conn, error) {
		raw, err := dial(ctx, "unix", bus)
		if err != nil {
			return nil, fmt.Errorf("open user bus %s: %w", bus, err)
		}

		stop := context.AfterFunc(ctx, func() { _ = raw.Close() })

		closeSockets = append(closeSockets, func() { stop(); _ = raw.Close() })

		conn, err := godbus.NewConn(raw)
		if err != nil {
			_ = raw.Close()

			return nil, err
		}

		if err := conn.Auth([]godbus.Auth{godbus.AuthExternal(strconv.Itoa(uid))}); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("authenticate to user bus: %w", err)
		}

		if err := conn.Hello(); err != nil {
			_ = conn.Close()

			return nil, err
		}

		return conn, nil
	})
	if err != nil {
		for _, closeSocket := range closeSockets {
			closeSocket()
		}

		return nil, err
	}

	return &Manager{conn: conn, closeSockets: closeSockets}, nil
}

func (m *Manager) Close() {
	m.conn.Close()

	for _, closeSocket := range m.closeSockets {
		closeSocket()
	}
}

func (m *Manager) Reload(ctx context.Context) error {
	return m.conn.ReloadContext(ctx)
}

func (m *Manager) Enable(ctx context.Context, units []string) error {
	if len(units) == 0 {
		return nil
	}

	_, _, err := m.conn.EnableUnitFilesContext(ctx, units, false, true)

	return err
}

func (m *Manager) Disable(ctx context.Context, units []string) error {
	if len(units) == 0 {
		return nil
	}

	_, err := m.conn.DisableUnitFilesContext(ctx, units, false)

	return err
}

// Loaded returns the units the manager knows, in the given order.
func (m *Manager) Loaded(ctx context.Context, units []string) ([]string, error) {
	if len(units) == 0 {
		return nil, nil
	}

	statuses, err := m.conn.ListUnitsByNamesContext(ctx, units)
	if err != nil {
		return nil, err
	}

	var loaded []string

	for _, status := range statuses {
		if status.LoadState != "not-found" {
			loaded = append(loaded, status.Name)
		}
	}

	return loaded, nil
}

func (m *Manager) Stop(ctx context.Context, units []string) error {
	return m.jobs(ctx, "stop", units, m.conn.StopUnitContext)
}

func (m *Manager) Restart(ctx context.Context, units []string) error {
	return m.jobs(ctx, "restart", units, m.conn.RestartUnitContext)
}

// TryRestart refreshes running on-demand services without starting inactive ones.
func (m *Manager) TryRestart(ctx context.Context, units []string) error {
	return m.jobs(ctx, "try-restart", units, m.conn.TryRestartUnitContext)
}

// jobs queues one job per unit and waits for every one of them, which is what
// systemctl does with several units on one command line.
func (m *Manager) jobs(ctx context.Context, operation string, units []string, submit func(context.Context, string, string, chan<- string) (int, error)) error {
	type job struct {
		unit   string
		result <-chan string
	}

	pending := make([]job, 0, len(units))

	for _, unit := range units {
		result := make(chan string, 1)
		if _, err := submit(ctx, unit, "replace", result); err != nil {
			return fmt.Errorf("systemd %s %s: %w", operation, unit, err)
		}

		pending = append(pending, job{unit: unit, result: result})
	}

	var failed []string

	for _, job := range pending {
		select {
		case result := <-job.result:
			if result != "done" && result != "skipped" {
				failed = append(failed, job.unit+": "+result)
			}
		case <-ctx.Done():
			return fmt.Errorf("systemd %s %s: %w", operation, job.unit, ctx.Err())
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("systemd %s failed: %s", operation, strings.Join(failed, "; "))
	}

	return nil
}
