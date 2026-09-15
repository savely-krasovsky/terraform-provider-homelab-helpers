// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package quadlet validates staged definitions using the host generator.
package quadlet

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/systemd"
)

type Host interface {
	host.Runner
	Stat(string) (fs.FileInfo, error)
	Mkdir(string, fs.FileMode) error
	ReadDir(string) ([]string, error)
}

type Validator struct{ Host Host }

func (v Validator) Discover(ctx context.Context, stage string) ([]string, error) {
	_, err := v.Host.Stat(path.Join(stage, "files/containers/systemd"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	if err == nil {
		if err := v.generate(ctx, stage); err != nil {
			return nil, err
		}
	}

	var units []string

	for _, directory := range []string{"generated", "files/systemd/user"} {
		names, err := v.Host.ReadDir(path.Join(stage, directory))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		for _, name := range names {
			info, err := v.Host.Stat(path.Join(stage, directory, name))
			if err != nil {
				return nil, err
			}

			if info.IsDir() {
				continue
			}

			if err := systemd.ValidateUnit(name); err != nil {
				return nil, err
			}

			if slices.Contains(units, name) {
				return nil, fmt.Errorf("both a native file and Quadlet generate %s", name)
			}

			units = append(units, name)
		}
	}

	slices.Sort(units)

	return units, nil
}

func (v Validator) generate(ctx context.Context, stage string) error {
	generator := ""

	for _, name := range []string{
		"/usr/libexec/podman/quadlet",
		"/usr/lib/systemd/system-generators/podman-system-generator",
	} {
		info, err := v.Host.Stat(name)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			generator = name

			break
		}
	}

	if generator == "" {
		return fmt.Errorf("cannot find the Podman Quadlet generator on the host")
	}

	generated := path.Join(stage, "generated")
	if err := v.Host.Mkdir(generated, 0700); err != nil {
		return err
	}

	_, err := v.Host.Run(ctx, host.Command{
		Name:          generator,
		Args:          []string{"--user", "--no-kmsg-log", generated},
		Env:           []string{"QUADLET_UNIT_DIRS=" + path.Join(stage, "files/containers/systemd")},
		CaptureStderr: true,
	})
	if err != nil {
		return fmt.Errorf("generate Quadlet units: %w", err)
	}

	return nil
}
