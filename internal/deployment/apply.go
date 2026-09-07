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

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

type Engine struct {
	Host  Host
	Paths Paths
}

// Apply accepts validated input and checks Quadlet generation and nftables on the host.
// The journal is written before importing secrets, and survives partial applies.
func (e Engine) Apply(ctx context.Context, payload Payload, values map[string]string) error {
	current := Ownership{
		Files: slices.Sorted(maps.Keys(payload.Files)),
		Units: unique(payload.Units),
	}
	old, owned, interrupted, err := e.readOwnership(current)
	if err != nil {
		return err
	}

	stage := path.Join(e.Paths.State(), ".homelab-stage-"+rand.Text())
	if err := e.Host.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer func() { _ = e.Host.RemoveStage(stage) }()

	if err := e.stage(ctx, stage, payload); err != nil {
		return fmt.Errorf("validate deployment: %w", err)
	}

	changed, drift, err := e.inspectFiles(payload, old)
	if err != nil {
		return err
	}
	refreshSecrets := interrupted || payload.SecretsRevision != old.SecretsRevision || len(e.missingSecrets(ctx, payload.Secrets)) > 0

	if err := writeJSON(ctx, e.Host, path.Join(e.Paths.State(), "config-pending.json"), owned); err != nil {
		return err
	}

	if refreshSecrets {
		if err := e.installSecrets(ctx, values); err != nil {
			return fmt.Errorf("import secrets (journal retained): %w", err)
		}
	}

	removedUnits := slices.DeleteFunc(slices.Clone(owned.Units), func(unit string) bool {
		return slices.Contains(payload.Units, unit)
	})
	if err := e.removeUnits(ctx, removedUnits); err != nil {
		return fmt.Errorf("remove units (journal retained for retry): %w", err)
	}

	for _, name := range owned.Files {
		if _, keep := payload.Files[name]; keep {
			continue
		}
		if err := e.Host.Remove(path.Join(e.Paths.Config(), name)); err != nil {
			return fmt.Errorf("remove configuration (journal retained for retry): %w", err)
		}
	}

	for _, name := range changed {
		if err := e.Host.InstallFile(ctx, path.Join(stage, "files", name), path.Join(e.Paths.Config(), name)); err != nil {
			return fmt.Errorf("install configuration (journal retained for retry): %w", err)
		}
	}

	// Always reapply the validated ruleset, including recovery after a partial apply.
	if err := e.applyFirewall(ctx, path.Join(stage, "firewall.nft")); err != nil {
		return fmt.Errorf("apply firewall (journal retained for retry): %w", err)
	}

	if err := e.applyUnits(ctx, payload.Groups, old.Groups, interrupted || drift, refreshSecrets); err != nil {
		return fmt.Errorf("apply units (journal retained for retry): %w", err)
	}

	next := manifest{
		Ownership:       current,
		Groups:          payload.Groups,
		SecretsRevision: payload.SecretsRevision,
		Revision:        Digest(payload),
		FileHashes:      make(map[string]string, len(payload.Files)),
	}
	for name, content := range payload.Files {
		next.FileHashes[name] = Digest(content)
	}
	if err := writeJSON(ctx, e.Host, path.Join(e.Paths.State(), "config-manifest.json"), next); err != nil {
		return err
	}

	return e.clearPending(ctx)
}

func (e Engine) readOwnership(current Ownership) (manifest, Ownership, bool, error) {
	var old manifest
	if _, err := readJSON(e.Host, path.Join(e.Paths.State(), "config-manifest.json"), &old); err != nil {
		return old, Ownership{}, false, err
	}

	var pending Ownership
	interrupted, err := readJSON(e.Host, path.Join(e.Paths.State(), "config-pending.json"), &pending)
	if err != nil {
		return old, Ownership{}, false, err
	}

	old.Files = slices.AppendSeq(old.Files, maps.Keys(old.FileHashes))

	owned := Ownership{
		Files: unique(old.Files, pending.Files, current.Files),
		Units: unique(old.Units, pending.Units, current.Units),
	}
	if err := owned.validate(); err != nil {
		return old, owned, interrupted, err
	}

	for _, name := range owned.Files {
		if err := e.Host.CheckPath(path.Join(e.Paths.Config(), name)); err != nil {
			return old, owned, interrupted, err
		}
	}

	return old, owned, interrupted, nil
}

func (e Engine) clearPending(ctx context.Context) error {
	if err := e.Host.Remove(path.Join(e.Paths.State(), "config-pending.json")); err != nil {
		return err
	}

	_, err := e.Host.Run(ctx, remote.Command{Name: "sync", Args: []string{"-f", e.Paths.State()}})

	return err
}
