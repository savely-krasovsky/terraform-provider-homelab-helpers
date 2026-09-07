// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
)

type Group struct {
	Units       []string `json:"units"`
	Enable      []string `json:"enable"`
	Hash        string   `json:"hash"`
	UsesSecrets bool     `json:"uses_secrets"`
}

type Payload struct {
	Secrets         map[string]string `json:"secrets"`
	Files           map[string]string `json:"files"`
	Units           []string          `json:"units"`
	Groups          map[string]Group  `json:"groups"`
	Firewall        string            `json:"firewall"`
	SecretsRevision string            `json:"secrets_revision"`
}

// Ownership keeps the Bash manifest/journal format so existing hosts can migrate in place.
type Ownership struct {
	Files []string `json:"files"`
	Units []string `json:"units"`
}

type manifest struct {
	Revision   string            `json:"revision,omitempty"`
	FileHashes map[string]string `json:"file_hashes,omitempty"`
	Ownership
	Groups          map[string]Group `json:"groups"`
	SecretsRevision string           `json:"secrets_revision"`
}

var (
	managedPath = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)
	managedUnit = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.@-]*\.(service|timer|socket)$`)
	secretName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
)

func (o Ownership) validate() error {
	for _, name := range o.Files {
		if !managedPath.MatchString(name) ||
			!fs.ValidPath(name) ||
			name == "." {
			return fmt.Errorf("invalid managed path: %q", name)
		}
	}

	for _, unit := range o.Units {
		if !managedUnit.MatchString(unit) {
			return fmt.Errorf("invalid managed unit: %q", unit)
		}
	}

	return nil
}

func (p Payload) Validate() error {
	names := map[string]bool{}
	for name, id := range p.Secrets {
		normalized := strings.ReplaceAll(name, "_", "-")
		if !secretName.MatchString(name) || strings.TrimSpace(id) == "" || id == "-" || names[normalized] {
			return fmt.Errorf("invalid or conflicting secret reference %q", name)
		}

		names[normalized] = true
	}

	if p.Firewall == "" {
		return fmt.Errorf("firewall must not be empty")
	}

	owned := Ownership{Files: slices.Collect(maps.Keys(p.Files)), Units: p.Units}
	if err := owned.validate(); err != nil {
		return err
	}

	for name, group := range p.Groups {
		if group.Hash == "" || len(group.Units) == 0 {
			return fmt.Errorf("invalid restart group %q", name)
		}

		for _, unit := range slices.Concat(group.Units, group.Enable) {
			if !slices.Contains(p.Units, unit) {
				return fmt.Errorf("group %q refers to unmanaged unit %q", name, unit)
			}
		}
	}

	return nil
}

func unique(values ...[]string) []string {
	all := slices.Concat(values...)
	slices.Sort(all)

	return slices.Compact(all)
}
