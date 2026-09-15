// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/systemd"
)

const (
	nativeDir  = "systemd/user/"
	quadletDir = "containers/systemd/"
)

// Payload has one activation boundary: every file and trigger belongs to the
// deployment as a whole.
type Payload struct {
	Files      map[string]string `json:"files"`
	Restart    []string          `json:"restart,omitempty"`
	TryRestart []string          `json:"try_restart,omitempty"`
	Enable     []string          `json:"enable,omitempty"`
	Triggers   map[string]string `json:"triggers,omitempty"`
}

var (
	managedPath = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)
)

func ValidatePath(name string) error {
	if !managedPath.MatchString(name) || !fs.ValidPath(name) || name == "." {
		return fmt.Errorf("invalid managed path: %q", name)
	}

	return nil
}

func Digest(value any) string {
	data, _ := json.Marshal(value) // Callers use only JSON-serializable descriptions.
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func (p Payload) Validate() error {
	for _, file := range slices.Sorted(maps.Keys(p.Files)) {
		if err := ValidatePath(file); err != nil {
			return err
		}

		for parent := path.Dir(file); parent != "."; parent = path.Dir(parent) {
			if _, exists := p.Files[parent]; exists {
				return fmt.Errorf("managed paths conflict: %s and %s", parent, file)
			}
		}

		if path.Dir(file) == strings.TrimSuffix(nativeDir, "/") {
			if err := systemd.ValidateUnit(path.Base(file)); err != nil {
				return err
			}
		}
	}

	for _, list := range []struct {
		name  string
		units []string
	}{{"restart", p.Restart}, {"try_restart", p.TryRestart}, {"enable", p.Enable}} {
		seen := map[string]bool{}

		for _, unit := range list.units {
			if err := systemd.ValidateUnit(unit); err != nil {
				return err
			}

			if seen[unit] {
				return fmt.Errorf("unit %q appears more than once in %s", unit, list.name)
			}

			seen[unit] = true
			if list.name == "enable" {
				if _, native := p.Files[nativeDir+unit]; !native {
					return fmt.Errorf("enable requires a native file at %s%s; Quadlet enablement belongs in [Install]", nativeDir, unit)
				}
			}
		}
	}

	for _, unit := range p.Restart {
		if slices.Contains(p.TryRestart, unit) {
			return fmt.Errorf("unit %q appears in both restart and try_restart", unit)
		}
	}

	return nil
}

func (p Payload) ValidateOwnership(units []string) error {
	for _, unit := range slices.Concat(p.Restart, p.TryRestart, p.Enable) {
		if !slices.Contains(units, unit) {
			return fmt.Errorf("activation refers to unowned unit %q: no native file or generated unit", unit)
		}
	}

	return nil
}
