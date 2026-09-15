// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"slices"
)

func (e Engine) applyUnits(ctx context.Context, desired Payload, owned, previouslyEnabled []string, activate bool) error {
	if activate {
		// Retired units were disabled before their files were removed. Reset the
		// remaining links so edits to native [Install] sections are reconciled.
		disable := slices.DeleteFunc(slices.Clone(previouslyEnabled), func(unit string) bool {
			return !slices.Contains(owned, unit)
		})
		if err := e.Units.Disable(ctx, disable); err != nil {
			return err
		}
	}

	if err := e.Units.Enable(ctx, desired.Enable); err != nil {
		return err
	}

	// Enable/DisableUnitFiles change links on disk, not the manager's loaded
	// dependency graph. Reload after both files and links are in their final form.
	if err := e.Units.Reload(ctx); err != nil {
		return err
	}

	if !activate {
		return nil
	}

	if err := e.Units.Restart(ctx, desired.Restart); err != nil {
		return err
	}

	return e.Units.TryRestart(ctx, desired.TryRestart)
}

func (e Engine) removeUnits(ctx context.Context, units, files []string) error {
	if len(units) == 0 {
		return nil
	}

	present, err := e.Units.Loaded(ctx, units)
	if err != nil {
		return err
	}

	if err := e.disableNative(ctx, units, files); err != nil {
		return err
	}

	if len(present) == 0 {
		return nil
	}

	return e.Units.Stop(ctx, present)
}

func (e Engine) disableNative(ctx context.Context, units, files []string) error {
	var native []string

	for _, unit := range units {
		if slices.Contains(files, nativeDir+unit) {
			native = append(native, unit)
		}
	}

	if len(native) == 0 {
		return nil
	}

	return e.Units.Disable(ctx, native)
}
