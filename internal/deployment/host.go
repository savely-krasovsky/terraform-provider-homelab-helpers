// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

type Host interface {
	Run(context.Context, remote.Command) ([]byte, error)
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	Mkdir(string, fs.FileMode) error
	Upload(string, []byte, fs.FileMode) error
	WriteFile(context.Context, string, []byte, fs.FileMode) error
	InstallFile(context.Context, string, string) error
	Remove(string) error
	RemoveStage(string) error
	CheckPath(string) error
}

type Paths struct {
	Home     string
	Firewall string
}

func (p Paths) Config() string { return path.Join(p.Home, ".config") }
func (p Paths) State() string  { return path.Join(p.Home, ".local/state/homelab") }

func (p Paths) Validate() error {
	for _, name := range []string{p.Home, p.Firewall} {
		if !path.IsAbs(name) || path.Clean(name) != name || name == "/" || strings.ContainsAny(name, "\x00\r\n") {
			return fmt.Errorf("expected a clean absolute host path: %q", name)
		}
	}

	return nil
}

func Prepare(ctx context.Context, h Host, p Paths) error {
	// Explicit parents repair older Ignition configurations without recursive
	// ownership changes to files or container data.
	uid, err := h.Run(ctx, remote.Command{Name: "id", Args: []string{"-u"}})
	if err != nil {
		return err
	}
	gid, err := h.Run(ctx, remote.Command{Name: "id", Args: []string{"-g"}})
	if err != nil {
		return err
	}

	dirs := []string{
		path.Join(p.Home, ".local"),
		path.Join(p.Home, ".local/bin"),
		path.Join(p.Home, ".local/state"),
		p.State(),
		p.Config(),
	}
	for _, dir := range dirs {
		if err := h.CheckPath(dir); err != nil {
			return err
		}

		mode := "0700"
		if dir == dirs[0] || dir == dirs[1] {
			mode = "0755"
		}

		if err := sudo(ctx, h, "install", "-d", "-o", strings.TrimSpace(string(uid)), "-g", strings.TrimSpace(string(gid)), "-m", mode, dir); err != nil {
			return err
		}
	}

	return sudo(ctx, h, "restorecon", dirs...)
}

func sudo(ctx context.Context, h Host, name string, args ...string) error {
	_, err := h.Run(ctx, remote.Command{Name: "sudo", Args: slices.Concat([]string{"-n", name}, args)})

	return err
}

func readJSON(h Host, name string, target any) (bool, error) {
	data, err := h.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := json.Unmarshal(data, target); err != nil {
		return true, fmt.Errorf("invalid deployment journal %s", name)
	}

	return true, nil
}

func writeJSON(ctx context.Context, h Host, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return h.WriteFile(ctx, name, data, 0600)
}
