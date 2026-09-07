// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

// ValidateSecretValues checks the complete batch before any host mutation.
// Values stay separate from Payload so fingerprints and journals cannot include them.
func ValidateSecretValues(refs, values map[string]string) error {
	if len(refs) != len(values) {
		return fmt.Errorf("secret_values_wo must contain exactly the names declared in secrets")
	}

	for name := range refs {
		if values[name] == "" {
			return fmt.Errorf("secret_values_wo has an empty or missing value for %q", name)
		}
	}

	return nil
}

func (e Engine) installSecrets(ctx context.Context, values map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(values)) {
		normalized := strings.ReplaceAll(name, "_", "-")
		var err error
		if strings.HasPrefix(name, "restic_") {
			err = e.installCredential(ctx, normalized, values[name])
		} else {
			_, err = e.Host.Run(ctx, remote.Command{
				Name:  "podman",
				Args:  []string{"secret", "create", "--replace", normalized, "-"},
				Stdin: strings.NewReader(values[name]),
			})
		}
		if err != nil {
			return fmt.Errorf("install secret %s: %w", name, err)
		}
	}

	return nil
}

func (e Engine) installCredential(ctx context.Context, name, value string) error {
	// Stage root credentials through stdin in /etc/credstore. Plaintext never
	// reaches a user-owned staging directory or the Terraform state.
	if err := sudo(ctx, e.Host, "install", "-d", "-m", "0700", "/etc/credstore"); err != nil {
		return err
	}

	tmp, err := e.rootTemp(ctx, "/etc/credstore")
	if err != nil {
		return err
	}
	defer e.removeRootTemp(ctx, tmp)

	if _, err := e.Host.Run(ctx, remote.Command{
		Name:  "sudo",
		Args:  []string{"-n", "tee", tmp},
		Stdin: strings.NewReader(value),
	}); err != nil {
		return err
	}

	return e.commitRootFile(ctx, tmp, path.Join("/etc/credstore", name))
}

func (e Engine) missingSecrets(ctx context.Context, refs map[string]string) []string {
	var missing []string
	for name := range refs {
		normalized := strings.ReplaceAll(name, "_", "-")
		var err error
		if strings.HasPrefix(name, "restic_") {
			err = sudo(ctx, e.Host, "test", "-s", path.Join("/etc/credstore", normalized))
		} else {
			_, err = e.Host.Run(ctx, remote.Command{Name: "podman", Args: []string{"secret", "inspect", normalized}})
		}
		if err != nil {
			missing = append(missing, "secret:"+name)
		}
	}

	return missing
}
