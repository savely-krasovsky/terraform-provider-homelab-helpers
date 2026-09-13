// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

// Quadlet resolves Volume= and Mount= into the final podman arguments, so the
// generated units name every bind mount source the host must provide. Named
// volumes and unexpanded systemd specifiers are not absolute paths and drop out.
var volumeArgument = regexp.MustCompile(`(?:^|\s)(?:-v|--volume)[ =]"?([^\s:"]+)|[\s,](?:source|src)=([^\s,"]+)`)

func (e Engine) stage(ctx context.Context, stage string, payload Payload) ([]string, error) {
	for _, name := range slices.Sorted(maps.Keys(payload.Files)) {
		if err := e.Host.Upload(path.Join(stage, "files", name), []byte(payload.Files[name]), 0644); err != nil {
			return nil, err
		}
	}

	sources, err := e.validateQuadlets(ctx, stage, payload.Units)
	if err != nil {
		return nil, err
	}

	firewall := path.Join(stage, "firewall.nft")
	if err := e.Host.Upload(firewall, []byte(payload.Firewall), 0600); err != nil {
		return nil, err
	}

	if err := sudo(ctx, e.Host, "nft", "--check", "--file", firewall); err != nil {
		return nil, fmt.Errorf("nftables validation failed: %w", err)
	}

	return sources, nil
}

func (e Engine) validateQuadlets(ctx context.Context, stage string, units []string) ([]string, error) {
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
		return nil, fmt.Errorf("cannot find the Podman Quadlet generator on the host")
	}

	generated := path.Join(stage, "generated")
	if err := e.Host.Mkdir(generated, 0700); err != nil {
		return nil, err
	}

	_, err := e.Host.Run(ctx, remote.Command{
		Name: generator,
		Args: []string{"--user", "--no-kmsg-log", generated},
		Env:  []string{"QUADLET_UNIT_DIRS=" + path.Join(stage, "files/containers/systemd")},
	})
	if err != nil {
		return nil, fmt.Errorf("generate Quadlet units: %w", err)
	}

	var sources []string
	for _, unit := range units {
		content, err := e.Host.ReadFile(path.Join(generated, unit))
		if err == nil {
			sources = append(sources, mountSources(string(content))...)

			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if _, err := e.Host.Stat(path.Join(stage, "files/systemd/user", unit)); err == nil {
			continue
		}

		return nil, fmt.Errorf("unit was not generated: %s", unit)
	}

	return unique(sources), nil
}

func mountSources(unit string) []string {
	var sources []string
	for _, match := range volumeArgument.FindAllStringSubmatch(unit, -1) {
		if source := match[1] + match[2]; path.IsAbs(source) {
			sources = append(sources, path.Clean(source))
		}
	}

	return sources
}
