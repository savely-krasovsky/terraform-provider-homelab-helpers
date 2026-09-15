// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package host

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStderrBoundsOutputAndPreservesCause(t *testing.T) {
	var stderr Stderr

	cause := errors.New("command failed")
	require.Same(t, cause, stderr.Wrap(cause))

	for range 3 {
		data := []byte(strings.Repeat("x", stderrLimit))
		n, err := stderr.Write(data)
		require.NoError(t, err)
		require.Len(t, data, n, "output must be drained even after reaching the limit")
	}

	err := stderr.Wrap(cause)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "[stderr truncated]")
	require.Len(t, stderr.data, stderrLimit)
	require.Less(t, len(err.Error()), stderrLimit+100)
}
