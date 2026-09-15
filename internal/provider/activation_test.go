// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func activationConfig(t *testing.T, schema *tfprotov6.Schema, revision any) (tftypes.Object, *tfprotov6.DynamicValue) {
	t.Helper()

	return object(t, schema, map[string]any{
		"name":     "apps",
		"files":    stringMap("systemd/user/worker.service", "[Service]\nExecStart=/bin/true\n", "settings/config", "expected"),
		"restart":  []tftypes.Value{tftypes.NewValue(tftypes.String, "worker.service")},
		"triggers": map[string]tftypes.Value{"credential": tftypes.NewValue(tftypes.String, revision)},
	})
}

func TestActivationRPCTriggerChangeAppliesAndThenPlansNoop(t *testing.T) {
	server, schema, _, units := lifecycleServer(t)
	typ, config := activationConfig(t, schema, "1")
	initial := planConfig(t, server, nullOf(t, typ), config, config)
	require.False(t, attributes(t, typ, initial.PlannedState)["units"].IsKnown())

	created, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: "quadlet_deployment", PriorState: nullOf(t, typ), PlannedState: initial.PlannedState, Config: config})
	require.NoError(t, err)
	require.Empty(t, created.Diagnostics)
	require.Equal(t, []string{"worker.service"}, units.restarted)
	require.Equal(t, tftypes.NewValue(typ.AttributeTypes["units"], []tftypes.Value{tftypes.NewValue(tftypes.String, "worker.service")}), attributes(t, typ, created.NewState)["units"])

	units.restarted = nil
	_, changed := activationConfig(t, schema, "2")
	proposed := attributes(t, typ, created.NewState)
	proposed["triggers"] = attributes(t, typ, changed)["triggers"]
	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, proposed))
	require.NoError(t, err)

	plan := planConfig(t, server, created.NewState, &dynamic, changed)
	require.NotEqual(t, attributes(t, typ, created.NewState)["applied_revision"], attributes(t, typ, plan.PlannedState)["applied_revision"])

	applied, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: "quadlet_deployment", PriorState: created.NewState, PlannedState: plan.PlannedState, Config: changed})
	require.NoError(t, err)
	require.Empty(t, applied.Diagnostics)
	require.Equal(t, []string{"worker.service"}, units.restarted)

	noop := planConfig(t, server, applied.NewState, applied.NewState, changed)
	require.Equal(t, attributes(t, typ, applied.NewState), attributes(t, typ, noop.PlannedState))
}

func TestUnknownTriggerDefersRevision(t *testing.T) {
	server, schema, _, _ := lifecycleServer(t)
	typ, config := activationConfig(t, schema, tftypes.UnknownValue)
	plan := planConfig(t, server, nullOf(t, typ), config, config)
	require.False(t, attributes(t, typ, plan.PlannedState)["applied_revision"].IsKnown())
}
