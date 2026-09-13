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
	if len(units) == 0 {
		return nil
	}

	states, err := e.Host.Run(ctx, remote.Command{
		Name: "systemctl",
		Args: slices.Concat([]string{"--user", "show", "--property=LoadState", "--value"}, units),
	})
	if err != nil {
		return err
	}

	lines := strings.Fields(string(states))
	var present, timers []string
	for i, unit := range units {
		if i < len(lines) && lines[i] == "not-found" {
			continue
		}
		present = append(present, unit)
		if strings.HasSuffix(unit, ".timer") {
			timers = append(timers, unit)
		}
	}

	if len(timers) > 0 {
		if _, err := e.Host.Run(ctx, remote.Command{
			Name: "systemctl",
			Args: slices.Concat([]string{"--user", "disable"}, timers),
		}); err != nil {
			return err
		}
	}

	if len(present) == 0 {
		return nil
	}

	// One job set lets systemd order the stops itself. Stopping unit by unit fights its
	// dependency graph: a network refuses to go down while a container still holds it.
	_, err = e.Host.Run(ctx, remote.Command{
		Name: "systemctl",
		Args: slices.Concat([]string{"--user", "stop"}, present),
	})

	return err
}
