// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"testing"

	hostio "github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
	"github.com/stretchr/testify/require"
)

func secondDeployment(engine Engine) (Engine, Payload) {
	engine.Name, engine.ID = "monitoring", "owner-monitoring"

	return engine, Payload{Files: map[string]string{
		"containers/systemd/metrics.container": "[Container]\nImage=metrics\n",
		"metrics/config":                       "metrics=true\n",
	}, Restart: []string{"metrics.service"}}
}

func TestDeploymentsOwnOnlyTheirFilesAndUnits(t *testing.T) {
	first, _, units, firstPayload := fixture(t)
	second, secondPayload := secondDeployment(first)
	require.NoError(t, apply(t.Context(), first, firstPayload))
	require.NoError(t, apply(t.Context(), second, secondPayload))

	for _, item := range []struct {
		engine  Engine
		payload Payload
	}{{first, firstPayload}, {second, secondPayload}} {
		snapshot, err := item.engine.Read(t.Context())
		require.NoError(t, err)
		require.True(t, snapshot.Found)
		require.Equal(t, item.payload.Files, snapshot.Files)
	}

	units.restarted = nil
	firstPayload.Files["app/settings.conf"] = "updated"
	require.NoError(t, apply(t.Context(), first, firstPayload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)

	units.stopped = nil

	require.NoError(t, first.Delete(t.Context()))
	require.ElementsMatch(t, []string{"app.service", "other.service"}, units.stopped)

	snapshot, err := second.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, secondPayload.Files, snapshot.Files)
}

func TestConflictsAreRejectedBeforeClaimingOrMutating(t *testing.T) {
	for name, file := range map[string]string{
		"same file":                     "app/settings.conf",
		"parent file":                   "app",
		"child file":                    "app/settings.conf/child",
		"same unit with another source": "systemd/user/app.service",
	} {
		t.Run(name, func(t *testing.T) {
			first, host, units, payload := fixture(t)
			require.NoError(t, apply(t.Context(), first, payload))

			second, desired := secondDeployment(first)

			desired.Files[file] = "conflict"
			host.installed, units.restarted, units.stopped = nil, nil, nil
			result, err := second.Apply(t.Context(), desired)
			require.ErrorContains(t, err, "deployment \"apps\"")
			require.False(t, result.Claimed)
			require.Empty(t, host.installed)
			require.Empty(t, units.restarted)
			require.Empty(t, units.stopped)
			require.NoFileExists(t, path.Join(second.StateDir(), recordFile))

			snapshot, err := first.Read(t.Context())
			require.NoError(t, err)
			require.Equal(t, payload.Files, snapshot.Files)
		})
	}
}

func TestAnotherResourceCannotTakeOverADeploymentName(t *testing.T) {
	engine, _, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	engine.ID = "different-owner"
	result, err := engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "owned by another resource")
	require.False(t, result.Claimed)

	_, err = engine.Read(t.Context())
	require.ErrorContains(t, err, "owned by another resource")
	require.ErrorContains(t, engine.Delete(t.Context()), "owned by another resource")
	require.Empty(t, units.stopped)
}

func TestRecoveryRemainsVisibleWhenFilesAlreadyMatch(t *testing.T) {
	engine, host, units, payload := fixture(t)
	other, otherPayload := secondDeployment(engine)
	require.NoError(t, apply(t.Context(), engine, payload))
	require.NoError(t, apply(t.Context(), other, otherPayload))
	require.NoError(t, host.WriteFile(t.Context(), path.Join(engine.Paths.Config, "app/settings.conf"), []byte("drift"), 0644))

	units.failRestart = true
	result, err := engine.Apply(t.Context(), payload)
	require.Error(t, err)
	require.True(t, result.Claimed)

	snapshot, err := engine.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, payload.Files, snapshot.Files)
	require.Empty(t, snapshot.Revision)

	otherSnapshot, err := other.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, Digest(otherPayload), otherSnapshot.Revision)

	units.failRestart, units.restarted = false, nil

	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, []string{"app.service", "other.service"}, units.restarted)

	snapshot, err = engine.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, Digest(payload), snapshot.Revision)
}

type uncertainJournalHost struct{ hostio.Files }

func (h uncertainJournalHost) WriteFile(ctx context.Context, name string, content []byte, mode fs.FileMode) error {
	if err := h.Files.WriteFile(ctx, name, content, mode); err != nil {
		return err
	}

	if path.Base(name) == recordFile {
		return errors.New("connection lost after journal write")
	}

	return nil
}

func TestUncertainCreateKeepsClaimsAndCanBeDeleted(t *testing.T) {
	engine, host, units, payload := fixture(t)
	engine.Host = uncertainJournalHost{host}
	result, err := engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "connection lost")
	require.True(t, result.Claimed)
	require.Empty(t, host.installed)

	engine.Host = host
	snapshot, err := engine.Read(t.Context())
	require.NoError(t, err)
	require.True(t, snapshot.Found)
	require.Empty(t, snapshot.Revision)

	other := engine
	other.Name, other.ID = "other", "other-owner"
	_, err = other.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "owned by deployment")
	require.NoError(t, engine.Delete(t.Context()))
	require.NotEmpty(t, units.stopped)
	require.NoError(t, apply(t.Context(), other, payload))
}

func TestMissingOwnershipCannotDeleteAnotherDeployment(t *testing.T) {
	engine, _, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	absent := engine
	absent.Name, absent.ID = "missing", "missing-owner"
	require.NoError(t, absent.Delete(t.Context()))
	require.Empty(t, units.stopped)
	require.FileExists(t, path.Join(engine.Paths.Config, "containers/systemd/app.container"))
}

func TestUnknownRecordFormatIsRejected(t *testing.T) {
	engine, host, _, payload := fixture(t)
	require.NoError(t, host.WriteFile(t.Context(), path.Join(engine.StateDir(), recordFile), []byte(`{"version":42,"id":"owner-apps"}`), 0600))

	_, err := engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "unsupported")
	require.Empty(t, host.installed)
}

func TestDeploymentNameCannotEscapeStateDirectory(t *testing.T) {
	engine, host, _, payload := fixture(t)
	for _, name := range []string{"", ".", "..", "../other", "/absolute", "with space"} {
		engine.Name = name
		result, err := engine.Apply(t.Context(), payload)
		require.ErrorContains(t, err, "invalid deployment name")
		require.False(t, result.Claimed)
	}

	require.Empty(t, host.installed)
}

func TestDeleteJournalSurvivesAnInterruptedDelete(t *testing.T) {
	engine, host, _, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	engine.Host = uncertainJournalHost{host}
	require.Error(t, engine.Delete(t.Context()))

	engine.Host = host
	snapshot, err := engine.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, payload.Files, snapshot.Files)
	require.Empty(t, snapshot.Revision)
	require.NoError(t, engine.Delete(t.Context()))

	_, err = os.Stat(path.Join(engine.StateDir(), recordFile))
	require.ErrorIs(t, err, fs.ErrNotExist)
}
