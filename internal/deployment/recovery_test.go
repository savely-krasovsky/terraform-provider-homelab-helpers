// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
)

type failedCommit struct {
	host.Files
	afterWrite bool
}

func (h failedCommit) WriteFile(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	var state record

	if path.Base(name) != recordFile {
		return h.Files.WriteFile(ctx, name, data, mode)
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	if state.Revision == "" {
		return h.Files.WriteFile(ctx, name, data, mode)
	}

	if h.afterWrite {
		if err := h.Files.WriteFile(ctx, name, data, mode); err != nil {
			return err
		}
	}

	return errors.New("commit response lost")
}

func TestCommitFailurePreservesRecoveryOrCompletedRevision(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before replacement", true: "after replacement"}[afterWrite], func(t *testing.T) {
			engine, files, units, payload := fixture(t)
			require.NoError(t, apply(t.Context(), engine, payload))

			payload.Files["app/added.conf"] = "new file"
			engine.Host = failedCommit{Files: files, afterWrite: afterWrite}
			require.ErrorContains(t, apply(t.Context(), engine, payload), "commit response lost")

			engine.Host = files
			snapshot, err := engine.Read(t.Context())
			require.NoError(t, err)
			require.Equal(t, payload.Files, snapshot.Files)

			if afterWrite {
				require.Equal(t, Digest(payload), snapshot.Revision)
			} else {
				require.Empty(t, snapshot.Revision)
			}

			units.restarted = nil

			require.NoError(t, apply(t.Context(), engine, payload))

			if afterWrite {
				require.Empty(t, units.restarted)
			} else {
				require.ElementsMatch(t, payload.Restart, units.restarted)
			}
		})
	}
}

func TestRecoveryRetiresFilesFromAnAbandonedUpdate(t *testing.T) {
	engine, files, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	payload.Files["app/abandoned.conf"] = "partially installed"
	units.failRestart = true

	require.Error(t, apply(t.Context(), engine, payload))

	// A new desired configuration must still clean up the unfinished update.
	delete(payload.Files, "app/abandoned.conf")

	units.failRestart = false

	require.NoError(t, apply(t.Context(), engine, payload))
	require.NoFileExists(t, path.Join(engine.Paths.Config, "app/abandoned.conf"))

	state, err := readRecord(files, engine.StateDir())
	require.NoError(t, err)
	require.NotContains(t, state.Files, "app/abandoned.conf")
	require.Equal(t, Digest(payload), state.Revision)
}
