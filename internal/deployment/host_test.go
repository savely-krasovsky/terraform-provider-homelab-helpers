// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"io/fs"
	"os"
	"path"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/local"
)

func TestPreparePreservesExistingConfigPermissions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("local transport requires Linux")
	}

	client, err := local.Connect(t.Context())
	require.NoError(t, err)
	t.Cleanup(client.Close)

	for _, mode := range []fs.FileMode{0700, 0750} {
		root := t.TempDir()
		paths := Paths{Config: path.Join(root, "config"), State: path.Join(root, "state")}
		require.NoError(t, os.Mkdir(paths.Config, mode))
		require.NoError(t, Prepare(t.Context(), client, paths))

		info, err := os.Stat(paths.Config)
		require.NoError(t, err)
		require.Equal(t, mode, info.Mode().Perm())
	}
}
