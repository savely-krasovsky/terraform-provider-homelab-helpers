// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/deployment"
	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/quadlet"
)

// A file-only deployment exercises the full Framework RPC lifecycle without
// requiring a real systemd manager or changing anything outside a temp directory.
type lifecycleHost struct{ host.Files }

func (*lifecycleHost) CheckPath(string) error { return nil }
func (*lifecycleHost) Stat(name string) (fs.FileInfo, error) {
	if name == "/usr/libexec/podman/quadlet" {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}

		return os.Stat(executable)
	}

	return os.Stat(name)
}
func (*lifecycleHost) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (*lifecycleHost) ReadDir(name string) ([]string, error) {
	entries, err := os.ReadDir(name)

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, err
}
func (*lifecycleHost) Mkdir(name string, mode fs.FileMode) error { return os.MkdirAll(name, mode) }
func (*lifecycleHost) Upload(name string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}

	return os.WriteFile(name, data, mode)
}
func (h *lifecycleHost) WriteFile(_ context.Context, name string, data []byte, mode fs.FileMode) error {
	return h.Upload(name, data, mode)
}
func (h *lifecycleHost) InstallFile(ctx context.Context, source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}

	return h.WriteFile(ctx, destination, data, 0644)
}
func (*lifecycleHost) Remove(name string) error {
	err := os.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}
func (*lifecycleHost) RemoveStage(name string) error { return os.RemoveAll(name) }
func (*lifecycleHost) Run(_ context.Context, command host.Command) ([]byte, error) {
	if command.Name == "/usr/libexec/podman/quadlet" || command.Name == "sync" {
		return nil, nil
	}

	return nil, errors.New("unexpected command: " + command.Name)
}

type lifecycleUnits struct {
	deployment.Units
	failReload bool
	restarted  []string
}

func (u *lifecycleUnits) Reload(context.Context) error {
	if u.failReload {
		return errors.New("activation failed")
	}

	return nil
}
func (*lifecycleUnits) Enable(context.Context, []string) error  { return nil }
func (*lifecycleUnits) Disable(context.Context, []string) error { return nil }
func (u *lifecycleUnits) Restart(_ context.Context, units []string) error {
	u.restarted = append(u.restarted, units...)
	return nil
}
func (*lifecycleUnits) TryRestart(context.Context, []string) error { return nil }
func (*lifecycleUnits) Loaded(_ context.Context, units []string) ([]string, error) {
	return units, nil
}
func (*lifecycleUnits) Stop(context.Context, []string) error { return nil }

type lifecycleAccess struct{ target deployment.Engine }

func (a lifecycleAccess) engine(ctx context.Context, _ bool, fn func(context.Context, deployment.Engine) error) error {
	return fn(ctx, a.target)
}

type lifecycleProvider struct {
	frameworkprovider.Provider
	access deploymentAccess
}

func (p lifecycleProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return &deploymentResource{access: p.access} }}
}

func (p lifecycleProvider) Configure(ctx context.Context, req frameworkprovider.ConfigureRequest, resp *frameworkprovider.ConfigureResponse) {
	p.Provider.Configure(ctx, req, resp)
	// Validate real provider configuration, while keeping the fixture's access.
	resp.ResourceData = nil
}

func lifecycleEngine(t *testing.T) (deployment.Engine, *lifecycleUnits) {
	t.Helper()

	root := t.TempDir()
	units := &lifecycleUnits{}
	engine := deployment.Engine{
		Host: &lifecycleHost{}, Quadlets: quadlet.Validator{Host: &lifecycleHost{}}, Units: units,
		Paths: deployment.Paths{Config: filepath.Join(root, "config"), State: filepath.Join(root, "state")},
	}
	require.NoError(t, deployment.Prepare(t.Context(), engine.Host, engine.Paths))

	return engine, units
}

func lifecycleServer(t *testing.T) (tfprotov6.ProviderServer, *tfprotov6.Schema, deployment.Engine, *lifecycleUnits) {
	t.Helper()

	engine, units := lifecycleEngine(t)
	server := providerserver.NewProtocol6(lifecycleProvider{Provider: New("test")(), access: lifecycleAccess{target: engine}})()
	schema, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, schema.Diagnostics)

	return server, schema.ResourceSchemas["quadlet_deployment"], engine, units
}

func attributes(t *testing.T, typ tftypes.Type, state *tfprotov6.DynamicValue) map[string]tftypes.Value {
	t.Helper()

	value, err := state.Unmarshal(typ)
	require.NoError(t, err)

	var result map[string]tftypes.Value
	require.NoError(t, value.As(&result))

	return result
}

func planConfig(t *testing.T, server tfprotov6.ProviderServer, prior, proposed, config *tfprotov6.DynamicValue) *tfprotov6.PlanResourceChangeResponse {
	t.Helper()

	plan, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: prior, ProposedNewState: proposed, Config: config,
	})
	require.NoError(t, err)
	require.Empty(t, plan.Diagnostics)

	return plan
}

func TestConfigRPCRecoversFailedActivationWithoutFileChanges(t *testing.T) {
	server, schema, engine, units := lifecycleServer(t)
	typ, config := object(t, schema, map[string]any{"name": "apps", "files": stringMap("settings.conf", "expected")})
	initial := planConfig(t, server, nullOf(t, typ), config, config)
	created, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: initial.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.Empty(t, created.Diagnostics)

	before := attributes(t, typ, created.NewState)
	require.NoError(t, os.WriteFile(filepath.Join(engine.Paths.Config, "settings.conf"), []byte("drift"), 0644))

	refreshed, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: "quadlet_deployment", CurrentState: created.NewState})
	require.NoError(t, err)
	require.Empty(t, refreshed.Diagnostics)

	proposed := attributes(t, typ, refreshed.NewState)
	proposed["files"] = before["files"]
	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, proposed))
	require.NoError(t, err)

	repair := planConfig(t, server, refreshed.NewState, &dynamic, config)
	units.failReload = true
	failed, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: refreshed.NewState, PlannedState: repair.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.NotEmpty(t, failed.Diagnostics)

	refreshed, err = server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: "quadlet_deployment", CurrentState: failed.NewState})
	require.NoError(t, err)
	require.Empty(t, refreshed.Diagnostics)

	pending := attributes(t, typ, refreshed.NewState)
	require.Equal(t, before["files"], pending["files"])
	require.Equal(t, tftypes.NewValue(tftypes.String, ""), pending["applied_revision"])

	retry := planConfig(t, server, refreshed.NewState, refreshed.NewState, config)
	require.Empty(t, retry.RequiresReplace)
	require.Equal(t, before["applied_revision"], attributes(t, typ, retry.PlannedState)["applied_revision"])

	units.failReload = false
	recovered, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: refreshed.NewState, PlannedState: retry.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.Empty(t, recovered.Diagnostics)

	noop := planConfig(t, server, recovered.NewState, recovered.NewState, config)
	require.Equal(t, attributes(t, typ, recovered.NewState), attributes(t, typ, noop.PlannedState))
}

func TestConfigRPCPartialCreateRetainsOwnerButConflictDoesNot(t *testing.T) {
	server, schema, _, units := lifecycleServer(t)
	typ, config := object(t, schema, map[string]any{"name": "apps", "files": stringMap("settings.conf", "expected")})
	plan := planConfig(t, server, nullOf(t, typ), config, config)
	units.failReload = true
	failed, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: plan.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.NotEmpty(t, failed.Diagnostics)

	state := attributes(t, typ, failed.NewState)
	require.True(t, state["id"].IsKnown())
	require.False(t, state["id"].IsNull())
	require.Equal(t, tftypes.NewValue(tftypes.String, ""), state["applied_revision"])
	// Import can recover the owner even when a crashed Create never returned state.
	imported, err := server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: "quadlet_deployment", ID: "apps"})
	require.NoError(t, err)
	require.Empty(t, imported.Diagnostics)
	require.Len(t, imported.ImportedResources, 1)

	importedState := imported.ImportedResources[0].State
	require.Equal(t, state["id"], attributes(t, typ, importedState)["id"])

	conflict, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: plan.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.NotEmpty(t, conflict.Diagnostics)

	nothing, err := conflict.NewState.Unmarshal(typ)
	require.NoError(t, err)
	require.True(t, nothing.IsNull())

	units.failReload = false
	deleted, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: importedState, PlannedState: nullOf(t, typ), Config: nullOf(t, typ),
	})
	require.NoError(t, err)
	require.Empty(t, deleted.Diagnostics)
}

func TestConfigNameChangeReplacesOwner(t *testing.T) {
	server, schema, _, _ := lifecycleServer(t)
	typ, config := object(t, schema, map[string]any{"name": "apps", "files": stringMap("settings.conf", "expected")})
	plan := planConfig(t, server, nullOf(t, typ), config, config)
	created, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: plan.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.Empty(t, created.Diagnostics)

	_, renamed := object(t, schema, map[string]any{"name": "renamed", "files": stringMap("settings.conf", "expected")})
	proposed := attributes(t, typ, created.NewState)
	proposed["name"] = tftypes.NewValue(tftypes.String, "renamed")
	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, proposed))
	require.NoError(t, err)

	replacement := planConfig(t, server, created.NewState, &dynamic, renamed)
	require.Len(t, replacement.RequiresReplace, 1)
	require.False(t, attributes(t, typ, replacement.PlannedState)["id"].IsKnown())
	// An unknown file map must not preserve the old owner's ID in a saved plan.
	_, unknownConfig := object(t, schema, map[string]any{"name": "renamed", "files": tftypes.UnknownValue})
	proposed["files"] = tftypes.NewValue(typ.AttributeTypes["files"], tftypes.UnknownValue)
	dynamic, err = tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, proposed))
	require.NoError(t, err)

	replacement = planConfig(t, server, created.NewState, &dynamic, unknownConfig)
	require.False(t, attributes(t, typ, replacement.PlannedState)["id"].IsKnown())
}

func (*lifecycleHost) Sync(context.Context, string) error { return nil }

func TestComputedUnitsRecoverARetiredUnitAfterFailedApply(t *testing.T) {
	server, schema, _, units := lifecycleServer(t)
	typ, config := object(t, schema, map[string]any{
		"name":    "app",
		"files":   stringMap("systemd/user/app.target", "[Unit]\n", "systemd/user/retired.service", "[Service]\nExecStart=/bin/true\n"),
		"restart": []tftypes.Value{tftypes.NewValue(tftypes.String, "app.target")},
	})
	initial := planConfig(t, server, nullOf(t, typ), config, config)
	require.False(t, attributes(t, typ, initial.PlannedState)["units"].IsKnown())

	created, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: initial.PlannedState, Config: config,
	})
	require.NoError(t, err)
	require.Empty(t, created.Diagnostics)

	_, changed := object(t, schema, map[string]any{
		"name": "app", "files": stringMap("systemd/user/app.target", "[Unit]\n"),
		"restart": []tftypes.Value{tftypes.NewValue(tftypes.String, "app.target")},
	})
	proposed := attributes(t, typ, created.NewState)
	proposed["files"] = attributes(t, typ, changed)["files"]
	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, proposed))
	require.NoError(t, err)

	update := planConfig(t, server, created.NewState, &dynamic, changed)
	units.failReload = true
	failed, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: created.NewState, PlannedState: update.PlannedState, Config: changed,
	})
	require.NoError(t, err)
	require.NotEmpty(t, failed.Diagnostics)

	refreshed, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: "quadlet_deployment", CurrentState: failed.NewState})
	require.NoError(t, err)
	require.Empty(t, refreshed.Diagnostics)

	pending := attributes(t, typ, refreshed.NewState)
	require.Equal(t, attributes(t, typ, changed)["files"], pending["files"])
	require.Equal(t, attributes(t, typ, created.NewState)["units"], pending["units"])

	retry := planConfig(t, server, refreshed.NewState, refreshed.NewState, changed)
	require.False(t, attributes(t, typ, retry.PlannedState)["units"].IsKnown())

	units.failReload = false
	recovered, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: refreshed.NewState, PlannedState: retry.PlannedState, Config: changed,
	})
	require.NoError(t, err)
	require.Empty(t, recovered.Diagnostics)
	require.Equal(t, tftypes.NewValue(typ.AttributeTypes["units"], []tftypes.Value{tftypes.NewValue(tftypes.String, "app.target")}), attributes(t, typ, recovered.NewState)["units"])

	imported, err := server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: "quadlet_deployment", ID: "app"})
	require.NoError(t, err)
	require.Empty(t, imported.Diagnostics)
	require.Equal(t, attributes(t, typ, recovered.NewState)["units"], attributes(t, typ, imported.ImportedResources[0].State)["units"])

	noop := planConfig(t, server, recovered.NewState, recovered.NewState, changed)
	require.Equal(t, attributes(t, typ, recovered.NewState), attributes(t, typ, noop.PlannedState))
}
