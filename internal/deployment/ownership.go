// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/systemd"
)

const (
	recordVersion = 3
	recordFile    = "deployment.json"
)

// Ownership includes everything an interrupted operation may have touched.
type Ownership struct {
	Files []string `json:"files"`
	Units []string `json:"units"`
}

// A record with an empty Revision reserves ownership for an unfinished operation.
// Committing replaces it with exactly the installed ownership and policy.
type record struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Ownership
	Enabled  []string `json:"enabled"`
	Revision string   `json:"applied_revision"`
}

var deploymentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

func ValidateName(name string) error {
	if !deploymentName.MatchString(name) {
		return fmt.Errorf("invalid deployment name %q: use 1–64 letters, digits, dots, underscores or hyphens, starting with a letter or digit", name)
	}

	return nil
}

// The caller holds config.lock while reading or changing records, files and units.
func (e Engine) StateDir() string {
	return path.Join(e.Paths.State, "deployments", e.Name)
}

// An absent record has an empty ID.
func readRecord(h host.Files, directory string) (record, error) {
	var state record

	found, err := readJSON(h, path.Join(directory, recordFile), &state)
	if err != nil || !found {
		return record{}, err
	}

	if state.Version != recordVersion || state.ID == "" {
		return record{}, fmt.Errorf("unsupported or incomplete deployment record in %s", directory)
	}

	for _, file := range state.Files {
		if err := ValidatePath(file); err != nil {
			return record{}, err
		}
	}

	for _, unit := range state.Units {
		if err := systemd.ValidateUnit(unit); err != nil {
			return record{}, err
		}
	}

	for _, unit := range state.Enabled {
		if !slices.Contains(state.Units, unit) {
			return record{}, fmt.Errorf("enablement refers to unowned unit %q in %s", unit, directory)
		}

		if !slices.Contains(state.Files, nativeDir+unit) {
			return record{}, fmt.Errorf("enablement refers to a non-native unit %q in %s", unit, directory)
		}
	}

	return state, nil
}

func (e Engine) readOwnership() (record, error) {
	if err := ValidateName(e.Name); err != nil {
		return record{}, err
	}

	if e.ID == "" {
		return record{}, fmt.Errorf("deployment owner ID must not be empty")
	}

	state, err := readRecord(e.Host, e.StateDir())
	if err != nil {
		return state, err
	}

	if state.ID != "" && state.ID != e.ID {
		return state, fmt.Errorf("deployment %q is owned by another resource; use a different name", e.Name)
	}

	return state, nil
}

func (e Engine) checkPaths(owned Ownership) error {
	for _, name := range owned.Files {
		if err := e.Host.CheckPath(path.Join(e.Paths.Config, name)); err != nil {
			return err
		}
	}

	return nil
}

// Both committed and unfinished records reserve their complete ownership.
func (e Engine) checkConflicts(owned Ownership) error {
	names, err := e.Host.ReadDir(path.Join(e.Paths.State, "deployments"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	slices.Sort(names)

	for _, name := range names {
		if name == e.Name {
			continue
		}

		if err := ValidateName(name); err != nil {
			return err
		}

		other, err := readRecord(e.Host, path.Join(e.Paths.State, "deployments", name))
		if err != nil {
			return err
		}

		for _, file := range owned.Files {
			for _, theirs := range other.Files {
				if file == theirs || strings.HasPrefix(file, theirs+"/") || strings.HasPrefix(theirs, file+"/") {
					return fmt.Errorf("file %q conflicts with %q owned by deployment %q", file, theirs, name)
				}
			}
		}

		for _, unit := range owned.Units {
			if slices.Contains(other.Units, unit) {
				return fmt.Errorf("unit %q is owned by deployment %q", unit, name)
			}
		}
	}

	return nil
}

func (e Engine) writeRecord(ctx context.Context, state record) error {
	state.Version, state.ID = recordVersion, e.ID
	return writeJSON(ctx, e.Host, path.Join(e.StateDir(), recordFile), state)
}

func unique(values ...[]string) []string {
	all := slices.Concat(values...)
	slices.Sort(all)

	return slices.Compact(all)
}
