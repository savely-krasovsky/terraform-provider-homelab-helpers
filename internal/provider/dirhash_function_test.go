// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirHashCompatibility(t *testing.T) {
	root := fixture(t)
	value, err := dirhash(root, "example1/**")
	require.NoError(t, err)
	require.Equal(t, "f0c5942360b2f167c8b99b3771f6f22a3b7c4e623046586b2f5aee730c4d1e31", value)

	again, err := dirhash(root, "example1/**")
	require.NoError(t, err)
	require.Equal(t, value, again)

	require.NoError(t, os.WriteFile(filepath.Join(root, "example1/example2/test.txt"), []byte("updated"), 0644))
	changed, err := dirhash(root, "example1/**")
	require.NoError(t, err)
	require.NotEqual(t, value, changed)
}

func TestDirHashEmptyArchive(t *testing.T) {
	value, err := dirhash(t.TempDir(), "**")
	require.NoError(t, err)
	require.Equal(t, "8739c76e681f900923b900c9df0ef75cf421d39cabb54650c4b9ad19b6a76d85", value)
}
