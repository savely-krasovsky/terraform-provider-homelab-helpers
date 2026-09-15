// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package systemd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJobsKeepUnitNamesWhenCompletionOrderDiffers(t *testing.T) {
	var manager Manager

	units := []string{"alpha.service", "beta.service", "gamma.service"}
	results := make(map[string]chan<- string)
	err := manager.jobs(t.Context(), "restart", units, func(_ context.Context, unit, mode string, result chan<- string) (int, error) {
		require.Equal(t, "replace", mode)

		results[unit] = result
		if len(results) == len(units) {
			// All jobs must be queued before waiting; systemd decides their order.
			results["gamma.service"] <- "dependency"

			results["beta.service"] <- "done"

			results["alpha.service"] <- "failed"
		}

		return len(results), nil
	})
	require.EqualError(t, err, "systemd restart failed: alpha.service: failed; gamma.service: dependency")
}

func TestJobsIdentifySubmissionErrorsAndCancellation(t *testing.T) {
	var manager Manager

	cause := errors.New("unit not found")
	err := manager.jobs(t.Context(), "stop", []string{"app.service"}, func(context.Context, string, string, chan<- string) (int, error) {
		return 0, cause
	})
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "systemd stop app.service")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = manager.jobs(ctx, "try-restart", []string{"app.service"}, func(context.Context, string, string, chan<- string) (int, error) {
		cancel()

		return 1, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "systemd try-restart app.service")
}
