// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package quadlet

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/local"
)

func TestGeneratorErrorIncludesFileAndReason(t *testing.T) {
	client := generatorClient(t)
	stage := t.TempDir()
	file := filepath.Join(stage, "files/containers/systemd/app.container")
	require.NoError(t, client.Upload(file, []byte("[Container]\nImage=example.invalid/app:1\nDefinitelyInvalidOption=yes\n"), 0644))

	_, err := (Validator{Host: client}).Discover(t.Context(), stage)
	require.ErrorContains(t, err, "app.container")
	require.ErrorContains(t, err, "DefinitelyInvalidOption")
}

func generatorClient(t *testing.T) *local.Client {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("local transport requires Linux")
	}

	available := false

	for _, candidate := range []string{"/usr/libexec/podman/quadlet", "/usr/lib/systemd/system-generators/podman-system-generator"} {
		if _, err := os.Stat(candidate); err == nil {
			available = true
			break
		}
	}

	if !available {
		if os.Getenv("REQUIRE_QUADLET_TESTS") == "1" {
			t.Fatal("the host Quadlet generator is required")
		}

		t.Skip("the host Quadlet generator is not available")
	}

	client, err := local.Connect(t.Context())
	require.NoError(t, err)

	t.Cleanup(client.Close)

	return client
}

func TestGeneratedAliasesAreOwned(t *testing.T) {
	client := generatorClient(t)
	stage := t.TempDir()
	file := filepath.Join(stage, "files/containers/systemd/app.container")
	require.NoError(t, client.Upload(file, []byte("[Container]\nImage=example.invalid/app:1\n[Install]\nAlias=app-alias.service\nWantedBy=default.target\n"), 0644))

	validator := Validator{Host: client}
	units, err := validator.Discover(t.Context(), stage)
	require.NoError(t, err)
	require.Equal(t, []string{"app-alias.service", "app.service"}, units)

	// Metadata may follow a generated alias; managed file access still refuses it.
	_, err = client.ReadFile(filepath.Join(stage, "generated/app-alias.service"))
	require.ErrorContains(t, err, "symlink")
	require.ErrorContains(t, client.WriteFile(t.Context(), filepath.Join(stage, "generated/app-alias.service"), []byte("replacement"), 0644), "symlink")

	require.NoError(t, client.Upload(filepath.Join(stage, "files/systemd/user/app-alias.service"), []byte("[Service]\nExecStart=/bin/true\n"), 0644))

	_, err = validator.Discover(t.Context(), stage)
	require.ErrorContains(t, err, "both a native file and Quadlet generate app-alias.service")
}
