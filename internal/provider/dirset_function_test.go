// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirSet(t *testing.T) {
	root := fixture(t)
	for _, tc := range []struct {
		directory string
		pattern   string
		want      []string
	}{
		{root, "**", []string{"example1", "example1/example2", "example3"}},
		{root + "/example1", "**", []string{"example2"}},
		{root, "example1/**", []string{"example1", "example1/example2"}},
		{root, "missing/**", []string{}},
	} {
		t.Run(tc.directory+tc.pattern, func(t *testing.T) {
			got, err := dirset(tc.directory, tc.pattern)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
