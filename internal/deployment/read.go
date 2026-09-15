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
	"slices"
)

// Snapshot separates installed bytes from completion of their activation.
// An empty Revision means an operation has not committed.
type Snapshot struct {
	Files    map[string]string
	Units    []string
	Revision string
	Found    bool
}

// Lookup recovers the recorded owner for an explicit import, including a create
// interrupted before Terraform received its state. Ordinary reads require an ID.
func (e Engine) Lookup(ctx context.Context) (string, Snapshot, error) {
	if err := ValidateName(e.Name); err != nil {
		return "", Snapshot{}, err
	}

	state, err := readRecord(e.Host, e.StateDir())
	if err != nil {
		return "", Snapshot{}, err
	}

	if state.ID == "" {
		return "", Snapshot{}, fmt.Errorf("deployment %q does not exist", e.Name)
	}

	e.ID = state.ID
	snapshot, err := e.Read(ctx)

	return state.ID, snapshot, err
}

func (e Engine) Read(context.Context) (Snapshot, error) {
	state, err := e.readOwnership()
	if err != nil || state.ID == "" {
		return Snapshot{}, err
	}

	result := Snapshot{Files: make(map[string]string, len(state.Files)), Found: true, Units: state.Units}
	result.Revision = state.Revision

	for _, name := range state.Files {
		content, err := e.Host.ReadFile(path.Join(e.Paths.Config, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return Snapshot{}, err
		}

		result.Files[name] = string(content)
	}

	return result, nil
}

// Delete uses only recorded ownership, never paths inferred from configuration.
// A stale resource cannot delete files belonging to a new owner with the same name.
func (e Engine) Delete(ctx context.Context) error {
	state, err := e.readOwnership()
	if err != nil || state.ID == "" {
		return err
	}

	if err := e.checkConflicts(state.Ownership); err != nil {
		return err
	}

	if err := e.checkPaths(state.Ownership); err != nil {
		return err
	}

	state.Revision = ""
	if err := e.writeRecord(ctx, state); err != nil {
		return err
	}

	if err := e.removeUnits(ctx, state.Units, state.Files); err != nil {
		return err
	}

	for _, name := range state.Files {
		if err := e.Host.Remove(path.Join(e.Paths.Config, name)); err != nil {
			return err
		}
	}

	if err := e.Units.Reload(ctx); err != nil {
		return err
	}

	if err := e.Host.Remove(path.Join(e.StateDir(), recordFile)); err != nil {
		return err
	}

	return e.Host.Sync(ctx, e.StateDir())
}

// inspectFiles lists the desired files whose host copy is missing or differs.
func (e Engine) inspectFiles(desired map[string]string) ([]string, error) {
	var changed []string

	for _, name := range slices.Sorted(maps.Keys(desired)) {
		content, err := e.Host.ReadFile(path.Join(e.Paths.Config, name))

		missing := errors.Is(err, fs.ErrNotExist)
		if err != nil && !missing {
			return nil, err
		}

		if missing || string(content) != desired[name] {
			changed = append(changed, name)
		}
	}

	return changed, nil
}
