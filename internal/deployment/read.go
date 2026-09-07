// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

func Digest(value any) string {
	// All callers supply only JSON-serializable strings, maps, slices and structs.
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// Read inspects files, journal, enabled timers and secret existence. It does not
// fetch secret values. External rotation uses an explicit secrets_revision bump.
func (e Engine) Read(ctx context.Context, payload Payload) (string, error) {
	old, _, pending, err := e.readOwnership(Ownership{})
	if err != nil {
		return "", err
	}

	drift, _, err := e.inspectFiles(payload, manifest{})
	if err != nil {
		return "", err
	}

	revision := Digest(payload)
	if pending || old.Revision != revision {
		drift = append(drift, "manifest")
	}

	// sha256sum avoids transferring the firewall during every refresh. It is
	// privileged because directory permissions can differ on existing hosts.
	sum, err := e.Host.Run(ctx, remote.Command{
		Name: "sudo",
		Args: []string{"-n", "sha256sum", "--", e.Paths.Firewall},
	})
	if err != nil {
		if check := sudo(ctx, e.Host, "test", "!", "-e", e.Paths.Firewall); check != nil {
			return "", fmt.Errorf("read firewall fingerprint: %w", err)
		}
	}
	expected := sha256.Sum256([]byte(payload.Firewall))
	if fields := strings.Fields(string(sum)); len(fields) == 0 || fields[0] != hex.EncodeToString(expected[:]) {
		drift = append(drift, "firewall")
	}

	for _, group := range payload.Groups {
		for _, unit := range group.Enable {
			result, err := e.Host.Run(ctx, remote.Command{Name: "systemctl", Args: []string{"--user", "is-enabled", unit}})
			if err != nil || strings.TrimSpace(string(result)) != "enabled" {
				drift = append(drift, unit)
			}
		}
	}

	drift = append(drift, e.missingSecrets(ctx, payload.Secrets)...)

	if err := ctx.Err(); err != nil {
		return "", err
	}

	if len(drift) > 0 {
		return "drift:" + Digest(unique(drift)), nil
	}

	return revision, nil
}

// Delete relinquishes managed files and stops their units. Host firewall and
// credentials are retained: they also serve bootstrap and backup services.
func (e Engine) Delete(ctx context.Context, current Ownership) error {
	_, owned, _, err := e.readOwnership(current)
	if err != nil {
		return err
	}

	if err := writeJSON(ctx, e.Host, path.Join(e.Paths.State(), "config-pending.json"), owned); err != nil {
		return err
	}

	if err := e.removeUnits(ctx, owned.Units); err != nil {
		return err
	}

	for _, name := range owned.Files {
		if err := e.Host.Remove(path.Join(e.Paths.Config(), name)); err != nil {
			return err
		}
	}

	if _, err := e.Host.Run(ctx, remote.Command{Name: "systemctl", Args: []string{"--user", "daemon-reload"}}); err != nil {
		return err
	}

	if err := e.Host.Remove(path.Join(e.Paths.State(), "config-manifest.json")); err != nil {
		return err
	}

	return e.clearPending(ctx)
}

// inspectFiles compares each file once, against both the desired content and
// the last successful apply. The latter catches drift in unchanged groups.
func (e Engine) inspectFiles(payload Payload, old manifest) (changed []string, drift bool, err error) {
	files := make([]string, 0, len(payload.Files)+len(old.FileHashes))
	files = slices.AppendSeq(files, maps.Keys(payload.Files))
	files = slices.AppendSeq(files, maps.Keys(old.FileHashes))
	slices.Sort(files)

	for _, name := range slices.Compact(files) {
		content, err := e.Host.ReadFile(path.Join(e.Paths.Config(), name))
		missing := errors.Is(err, fs.ErrNotExist)
		if err != nil && !missing {
			return nil, false, err
		}

		if desired, managed := payload.Files[name]; managed && (missing || string(content) != desired) {
			changed = append(changed, name)
		}
		if hash, recorded := old.FileHashes[name]; recorded && (missing || Digest(string(content)) != hash) {
			drift = true
		}
	}

	// Bash manifests did not record individual file hashes.
	return changed, drift || (old.FileHashes == nil && len(changed) > 0), nil
}
