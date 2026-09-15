// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"path"
	"slices"
)

type ApplyResult struct {
	// Claimed is true once writing the ownership record has been attempted.
	// A caller must retain the owner ID even if the write's outcome is uncertain.
	Claimed bool
	Units   []string
}

// Apply validates the tree with the host's Quadlet generator, records ownership
// in a record that survives a partial apply, replaces changed files atomically
// and activates the deployment when any file, trigger or policy changes.
func (e Engine) Apply(ctx context.Context, payload Payload) (result ApplyResult, err error) {
	if err := payload.Validate(); err != nil {
		return result, err
	}

	state, err := e.readOwnership()
	if err != nil {
		return result, err
	}

	current := Ownership{Files: slices.Sorted(maps.Keys(payload.Files))}

	stage := path.Join(e.Paths.State, ".terraform-stage-"+rand.Text())
	if err := e.Host.Mkdir(stage, 0700); err != nil {
		return result, err
	}

	defer func() { _ = e.Host.RemoveStage(stage) }()

	for _, name := range current.Files {
		if err := e.Host.Upload(path.Join(stage, "files", name), []byte(payload.Files[name]), 0644); err != nil {
			return result, err
		}
	}

	units, err := e.Quadlets.Discover(ctx, stage)
	if err != nil {
		return result, fmt.Errorf("validate deployment: %w", err)
	}

	if err := payload.ValidateOwnership(units); err != nil {
		return result, err
	}

	current.Units = units
	result.Units = units

	owned := Ownership{Files: unique(state.Files, current.Files), Units: unique(state.Units, units)}
	if err := e.checkConflicts(owned); err != nil {
		return result, err
	}

	if err := e.checkPaths(owned); err != nil {
		return result, err
	}

	interrupted := state.ID != "" && state.Revision == ""

	changed, err := e.inspectFiles(payload.Files)
	if err != nil {
		return result, err
	}

	if err := e.Host.Mkdir(e.StateDir(), 0700); err != nil {
		return result, err
	}

	result.Claimed = true
	if err := e.writeRecord(ctx, record{Ownership: owned, Enabled: state.Enabled}); err != nil {
		return result, err
	}

	removedUnits := slices.DeleteFunc(slices.Clone(owned.Units), func(unit string) bool {
		return slices.Contains(units, unit)
	})
	if err := e.removeUnits(ctx, removedUnits, owned.Files); err != nil {
		return result, fmt.Errorf("remove units (ownership retained for retry): %w", err)
	}

	for _, name := range owned.Files {
		if _, keep := payload.Files[name]; keep {
			continue
		}

		if err := e.Host.Remove(path.Join(e.Paths.Config, name)); err != nil {
			return result, fmt.Errorf("remove configuration (ownership retained for retry): %w", err)
		}
	}

	for _, name := range changed {
		if err := e.Host.InstallFile(ctx, path.Join(stage, "files", name), path.Join(e.Paths.Config, name)); err != nil {
			return result, fmt.Errorf("install configuration (ownership retained for retry): %w", err)
		}
	}

	// A partial create may have enabled units before a revision was committed.
	// Reset those links from recorded ownership before applying current policy.
	if interrupted {
		if err := e.disableNative(ctx, owned.Units, owned.Files); err != nil {
			return result, fmt.Errorf("recover enablement (ownership retained for retry): %w", err)
		}
	}

	revision := Digest(payload)
	activate := interrupted || len(changed) > 0 || state.Revision != revision || !slices.Equal(state.Units, units)

	if err := e.applyUnits(ctx, payload, units, state.Enabled, activate); err != nil {
		return result, fmt.Errorf("apply units (ownership retained for retry): %w", err)
	}

	return result, e.writeRecord(ctx, record{
		Ownership: current,
		Revision:  revision,
		Enabled:   payload.Enable,
	})
}
