// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"path"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// Native [Install] links affect a loaded target only after a manager reload.
type linkedTarget struct {
	Units
	links, loaded, started []string
}

func (u *linkedTarget) Enable(_ context.Context, names []string) error {
	u.links = unique(u.links, names)
	return nil
}

func (u *linkedTarget) Disable(_ context.Context, names []string) error {
	u.links = slices.DeleteFunc(u.links, func(name string) bool { return slices.Contains(names, name) })
	return nil
}

func (u *linkedTarget) Reload(context.Context) error {
	u.loaded = slices.Clone(u.links)
	return nil
}

func (u *linkedTarget) Restart(context.Context, []string) error {
	u.started = slices.Clone(u.loaded)
	return nil
}

func (*linkedTarget) TryRestart(context.Context, []string) error { return nil }

func TestTargetActivationUsesUpdatedEnablement(t *testing.T) {
	units := &linkedTarget{}
	engine := Engine{Units: units}
	owned := []string{"app.target", "first.service", "second.service"}

	var previous []string

	for _, enabled := range [][]string{{"first.service"}, {"second.service"}, {}} {
		payload := Payload{Restart: []string{"app.target"}, Enable: enabled}
		require.NoError(t, engine.applyUnits(t.Context(), payload, owned, previous, true))
		require.ElementsMatch(t, enabled, units.started)

		previous = enabled
	}
}

func TestAddingAndRemovingAnApplicationFileActivatesTheDeployment(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	units.restarted = nil
	payload.Files["new-directory/settings.conf"] = "new configuration"
	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, payload.Restart, units.restarted)

	units.restarted, host.installed = nil, nil

	delete(payload.Files, "new-directory/settings.conf")
	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, payload.Restart, units.restarted)
	require.Empty(t, host.installed)
	require.NoFileExists(t, path.Join(engine.Paths.Config, "new-directory/settings.conf"))
}

func TestChangingActivationPolicyNeedsNoFileChange(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	units.restarted, host.installed = nil, nil
	payload.Restart = []string{"other.service"}
	payload.TryRestart = []string{"app.service"}
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"other.service"}, units.restarted)
	require.Equal(t, []string{"app.service"}, units.conditional)
	require.Empty(t, host.installed)

	units.restarted, units.conditional = nil, nil

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Empty(t, units.restarted)
	require.Empty(t, units.conditional)
}

func TestNativeActivationAndEnablementLifecycle(t *testing.T) {
	engine, _, units, _ := fixture(t)
	payload := Payload{Files: map[string]string{
		nativeDir + "web.service": "[Service]\nExecStart=/bin/true\n",
		nativeDir + "http.socket": "[Socket]\nService=web.service\nListenStream=8080\n[Install]\nWantedBy=sockets.target\n",
		nativeDir + "job.service": "[Service]\nExecStart=/bin/true\n",
		nativeDir + "daily.timer": "[Timer]\nUnit=job.service\nOnCalendar=daily\n[Install]\nWantedBy=timers.target\n",
	},
		Restart: []string{"daily.timer", "http.socket"}, TryRestart: []string{"web.service"}, Enable: []string{"daily.timer", "http.socket"},
	}

	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, []string{"http.socket", "daily.timer"}, units.restarted)
	require.Equal(t, []string{"web.service"}, units.conditional)
	require.ElementsMatch(t, []string{"http.socket", "daily.timer"}, units.enabled)

	units.restarted, units.conditional = nil, nil

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Empty(t, units.restarted)
	require.Empty(t, units.conditional)

	payload.Files[nativeDir+"job.service"] += "# changed\n"
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"daily.timer", "http.socket"}, units.restarted)

	units.disabled = nil
	payload.Enable = []string{"daily.timer"}
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Contains(t, units.disabled, "http.socket")

	units.disabled = nil

	require.NoError(t, engine.Delete(t.Context()))
	require.ElementsMatch(t, []string{"http.socket", "web.service", "job.service", "daily.timer"}, units.disabled)
	require.NoFileExists(t, path.Join(engine.Paths.Config, nativeDir, "http.socket"))
}

func TestExplicitTriggerAndMountDriftActivateTheDeployment(t *testing.T) {
	engine, host, units, payload := fixture(t)
	payload.Files[quadletDir+"app.container"] = "[Container]\nImage=app\nMount=type=bind,source=%E/app/settings.conf,destination=/settings\n"
	payload.Triggers["credential"] = "1"
	require.NoError(t, apply(t.Context(), engine, payload))

	units.restarted = nil
	payload.Triggers["credential"] = "2"
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)

	units.restarted = nil

	require.NoError(t, host.WriteFile(t.Context(), path.Join(engine.Paths.Config, "app/settings.conf"), []byte("drift"), 0644))
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)
}

func TestRecoveryCleansEnablementFromFailedCreate(t *testing.T) {
	engine, _, units, _ := fixture(t)
	payload := Payload{Files: map[string]string{nativeDir + "worker.service": "[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=default.target\n"}, Restart: []string{"worker.service"}, Enable: []string{"worker.service"}}
	units.failRestart = true

	require.Error(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"worker.service"}, units.enabled)

	payload.Enable = nil
	units.failRestart = false

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Contains(t, units.disabled, "worker.service")
	require.FileExists(t, path.Join(engine.StateDir(), recordFile))
}

func TestRecordedEnablementCannotEscapeOwnership(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	var record record

	_, err := readJSON(host, path.Join(engine.StateDir(), recordFile), &record)
	require.NoError(t, err)

	record.Enabled = []string{"unmanaged.service"}
	require.NoError(t, writeJSON(t.Context(), host, path.Join(engine.StateDir(), recordFile), record))

	_, err = engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "unowned unit")
	require.Empty(t, units.disabled)
}

func TestActivationCannotEscapeDiscoveredOwnership(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		engine, host, units, payload := fixture(t)
		if conditional {
			payload.TryRestart = []string{"unmanaged.service"}
		} else {
			payload.Restart = []string{"unmanaged.service"}
		}

		result, err := engine.Apply(t.Context(), payload)
		require.ErrorContains(t, err, "unowned unit")
		require.False(t, result.Claimed)
		require.Empty(t, host.installed)
		require.Empty(t, units.restarted)
		require.Empty(t, units.conditional)
		require.NoFileExists(t, path.Join(engine.StateDir(), recordFile))
	}
}

func TestNativeAndGeneratedUnitCollisionIsRejected(t *testing.T) {
	engine, host, units, payload := fixture(t)
	payload.Files[nativeDir+"app.service"] = "[Service]\nExecStart=/bin/true\n"
	result, err := engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "both a native file and Quadlet")
	require.False(t, result.Claimed)
	require.Empty(t, host.installed)
	require.Empty(t, units.restarted)
}

func TestNewlyDiscoveredUnitsAreReservedDuringRecovery(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	host.strayUnit = "generated.service"
	units.failRestart = true
	result, err := engine.Apply(t.Context(), payload)
	require.ErrorContains(t, err, "restart failed")
	require.True(t, result.Claimed)
	require.Contains(t, result.Units, "generated.service")

	other, desired := secondDeployment(engine)
	desired.Files[nativeDir+"generated.service"] = "[Service]\nExecStart=/bin/true\n"
	host.strayUnit = ""
	result, err = other.Apply(t.Context(), desired)
	require.ErrorContains(t, err, "owned by deployment")
	require.False(t, result.Claimed)

	units.failRestart = false

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Contains(t, units.stopped, "generated.service")
	require.NoError(t, apply(t.Context(), other, desired))
}

func TestTargetsAreOwnedActivatedEnabledAndRetired(t *testing.T) {
	engine, _, units, payload := fixture(t)
	payload.Files[nativeDir+"app.target"] = "[Unit]\nWants=app.service other.service\n[Install]\nWantedBy=default.target\n"
	payload.Restart = []string{"app.target"}
	payload.Enable = []string{"app.target"}
	result, err := engine.Apply(t.Context(), payload)
	require.NoError(t, err)
	require.Equal(t, []string{"app.service", "app.target", "other.service"}, result.Units)
	require.Equal(t, []string{"app.target"}, units.restarted)
	require.Equal(t, []string{"app.target"}, units.enabled)
	delete(payload.Files, nativeDir+"app.target")

	payload.Restart, payload.Enable = nil, nil
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.target"}, units.stopped)
	require.Contains(t, units.disabled, "app.target")
}
