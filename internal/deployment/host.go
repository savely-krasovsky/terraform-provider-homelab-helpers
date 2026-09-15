// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package deployment applies a user configuration tree to a rootless Podman host.
package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
)

type Quadlets interface {
	Discover(context.Context, string) ([]string, error)
}

// Units is the systemd user manager.
type Units interface {
	Reload(context.Context) error
	Enable(context.Context, []string) error
	Disable(context.Context, []string) error
	Loaded(context.Context, []string) ([]string, error)
	Stop(context.Context, []string) error
	Restart(context.Context, []string) error
	TryRestart(context.Context, []string) error
}

type Engine struct {
	Host     host.Files
	Quadlets Quadlets
	Units    Units
	Paths    Paths
	Name     string
	ID       string
}

type Paths struct {
	Config string
	State  string
}

func Prepare(_ context.Context, h host.Files, p Paths) error {
	if err := h.CheckPath(p.Config); err != nil {
		return err
	}

	info, err := h.Stat(p.Config)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := h.Mkdir(p.Config, 0755); err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("configuration path is not a directory: %s", p.Config)
	}

	return h.Mkdir(p.State, 0700)
}

func readJSON(h host.Files, name string, target any) (bool, error) {
	data, err := h.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if err := json.Unmarshal(data, target); err != nil {
		return true, fmt.Errorf("invalid deployment record %s", name)
	}

	return true, nil
}

func writeJSON(ctx context.Context, h host.Files, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return h.WriteFile(ctx, name, data, 0600)
}
