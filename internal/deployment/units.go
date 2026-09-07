// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

func (e Engine) applyUnits(ctx context.Context, groups, previous map[string]Group, restartAll, refreshSecrets bool) error {
	var restart, enable []string
	for name, group := range groups {
		// Interrupted applies and out-of-band edits must retry the service transaction.
		if restartAll || group.Hash != previous[name].Hash || (group.UsesSecrets && refreshSecrets) {
			restart = append(restart, group.Units...)
		}

		enable = append(enable, group.Enable...)
	}

	if _, err := e.Host.Run(ctx, remote.Command{Name: "systemctl", Args: []string{"--user", "daemon-reload"}}); err != nil {
		return err
	}

	if len(enable) > 0 {
		if _, err := e.Host.Run(ctx, remote.Command{
			Name: "systemctl",
			Args: slices.Concat([]string{"--user", "enable"}, unique(enable)),
		}); err != nil {
			return err
		}
	}

	if len(restart) == 0 {
		return nil
	}

	// Submit one transaction so systemd orders related stacks together.
	_, err := e.Host.Run(ctx, remote.Command{
		Name: "systemctl",
		Args: slices.Concat([]string{"--user", "restart"}, unique(restart)),
	})

	return err
}

func (e Engine) removeUnits(ctx context.Context, units []string) error {
	for _, unit := range units {
		state, err := e.Host.Run(ctx, remote.Command{
			Name: "systemctl",
			Args: []string{"--user", "show", unit, "--property=LoadState", "--value"},
		})
		if err != nil {
			return err
		}

		if strings.TrimSpace(string(state)) == "not-found" {
			continue
		}

		if strings.HasSuffix(unit, ".timer") {
			if _, err := e.Host.Run(ctx, remote.Command{Name: "systemctl", Args: []string{"--user", "disable", unit}}); err != nil {
				return err
			}
		}

		if _, err := e.Host.Run(ctx, remote.Command{Name: "systemctl", Args: []string{"--user", "stop", unit}}); err != nil {
			return err
		}
	}

	return nil
}
