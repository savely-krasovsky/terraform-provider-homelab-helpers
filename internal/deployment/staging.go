// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

func (e Engine) stage(ctx context.Context, stage string, payload Payload) error {
	for _, name := range slices.Sorted(maps.Keys(payload.Files)) {
		if err := e.Host.Upload(path.Join(stage, "files", name), []byte(payload.Files[name]), 0644); err != nil {
			return err
		}
	}

	if err := e.validateQuadlets(ctx, stage, payload.Units); err != nil {
		return err
	}

	firewall := path.Join(stage, "firewall.nft")
	if err := e.Host.Upload(firewall, []byte(payload.Firewall), 0600); err != nil {
		return err
	}

	if err := sudo(ctx, e.Host, "nft", "--check", "--file", firewall); err != nil {
		return fmt.Errorf("nftables validation failed: %w", err)
	}

	return nil
}

func (e Engine) validateQuadlets(ctx context.Context, stage string, units []string) error {
	generator := ""
	for _, name := range []string{
		"/usr/libexec/podman/quadlet",
		"/usr/lib/systemd/system-generators/podman-system-generator",
	} {
		info, err := e.Host.Stat(name)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			generator = name

			break
		}
	}
	if generator == "" {
		return fmt.Errorf("cannot find the Podman Quadlet generator on the host")
	}

	generated := path.Join(stage, "generated")
	if err := e.Host.Mkdir(generated, 0700); err != nil {
		return err
	}

	_, err := e.Host.Run(ctx, remote.Command{
		Name: generator,
		Args: []string{"--user", "--no-kmsg-log", generated},
		Env:  []string{"QUADLET_UNIT_DIRS=" + path.Join(stage, "files/containers/systemd")},
	})
	if err != nil {
		return fmt.Errorf("generate Quadlet units: %w", err)
	}

	for _, unit := range units {
		if _, err := e.Host.Stat(path.Join(generated, unit)); err == nil {
			continue
		}
		if _, err := e.Host.Stat(path.Join(stage, "files/systemd/user", unit)); err == nil {
			continue
		}

		return fmt.Errorf("unit was not generated: %s", unit)
	}

	return nil
}
