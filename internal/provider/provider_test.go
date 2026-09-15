// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"maps"
	"runtime"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func server(t *testing.T) (tfprotov6.ProviderServer, *tfprotov6.GetProviderSchemaResponse) {
	t.Helper()

	server := providerserver.NewProtocol6(New("test")())()
	schema, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, schema.Diagnostics)

	return server, schema
}

// object leaves omitted attributes null.
func object(t *testing.T, schema *tfprotov6.Schema, values map[string]any) (tftypes.Object, *tfprotov6.DynamicValue) {
	t.Helper()

	typ, ok := schema.ValueType().(tftypes.Object)
	require.True(t, ok)

	attributes := map[string]tftypes.Value{}
	for name, attributeType := range typ.AttributeTypes {
		attributes[name] = tftypes.NewValue(attributeType, nil)
	}

	for name, value := range values {
		attributes[name] = tftypes.NewValue(typ.AttributeTypes[name], value)
	}

	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, attributes))
	require.NoError(t, err)

	return typ, &dynamic
}

func stringMap(values ...string) map[string]tftypes.Value {
	result := map[string]tftypes.Value{}
	for i := 0; i+1 < len(values); i += 2 {
		result[values[i]] = tftypes.NewValue(tftypes.String, values[i+1])
	}

	return result
}

func TestProviderSchema(t *testing.T) {
	_, schema := server(t)
	require.Equal(t,
		[]string{"quadlet_deployment", "quadlet_podman_secret"},
		slices.Sorted(maps.Keys(schema.ResourceSchemas)),
	)
	require.Empty(t, schema.Functions)
	require.Empty(t, schema.DataSourceSchemas)
}

func TestProviderConfigureValidation(t *testing.T) {
	for name, values := range map[string]map[string]any{
		"blank host":          {"host": " "},
		"missing ssh host":    {"user": "deploy"},
		"missing ssh user":    {"host": "example.test"},
		"blank ssh user":      {"host": "example.test", "user": " "},
		"invalid transport":   {"transport": "http"},
		"local with ssh host": {"transport": "local", "host": "example.test"},
		"local with ssh user": {"transport": "local", "user": "core"},
		"invalid socket":      {"host": "example.test", "podman_socket": "relative"},
		"invalid bus":         {"host": "example.test", "systemd_bus": "/run/../bus"},
		"bad port":            {"host": "example.test", "port": 70000},
		"bad timeout":         {"host": "example.test", "timeout": "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			server, schema := server(t)
			_, config := object(t, schema.Provider, values)
			response, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: config})
			require.NoError(t, err)
			require.NotEmpty(t, response.Diagnostics)
		})
	}

	server, schema := server(t)
	_, config := object(t, schema.Provider, map[string]any{"host": "example.test", "user": "deploy"})
	response, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: config})
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)
}

func TestConfigValidationRejects(t *testing.T) {
	for name, files := range map[string]map[string]tftypes.Value{
		"invalid managed path": stringMap("../escape", ""),
		"invalid managed unit": stringMap("systemd/user/app.mount", "[Mount]\n"),
	} {
		t.Run(name, func(t *testing.T) {
			server, schema := server(t)
			_, config := object(t, schema.ResourceSchemas["quadlet_deployment"], map[string]any{"name": "apps", "files": files})
			response, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: "quadlet_deployment", Config: config})
			require.NoError(t, err)
			require.Len(t, response.Diagnostics, 1)
			require.Contains(t, response.Diagnostics[0].Detail, name)
		})
	}
}

func TestSecretPlanKeepsTheValueOut(t *testing.T) {
	for _, resource := range []string{"quadlet_podman_secret"} {
		t.Run(resource, func(t *testing.T) {
			server, schema := server(t)
			typ, config := object(t, schema.ResourceSchemas[resource], map[string]any{
				"name": "app-password", "value_wo": "private-value",
			})
			response, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
				TypeName: resource, PriorState: nullOf(t, typ), ProposedNewState: config, Config: config,
			})
			require.NoError(t, err)
			require.Empty(t, response.Diagnostics)

			planned, err := response.PlannedState.Unmarshal(typ)
			require.NoError(t, err)

			var values map[string]tftypes.Value
			require.NoError(t, planned.As(&values))
			require.True(t, values["value_wo"].IsNull())
			require.Equal(t, tftypes.NewValue(tftypes.String, "1"), values["version"])

			_, invalid := object(t, schema.ResourceSchemas[resource], map[string]any{"name": "bad name", "value_wo": ""})
			validation, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{
				TypeName: resource, Config: invalid,
				ClientCapabilities: &tfprotov6.ValidateResourceConfigClientCapabilities{WriteOnlyAttributesAllowed: true},
			})
			require.NoError(t, err)
			require.Len(t, validation.Diagnostics, 2)
		})
	}
}

func nullOf(t *testing.T, typ tftypes.Type) *tfprotov6.DynamicValue {
	t.Helper()

	value, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
	require.NoError(t, err)

	return &value
}

func TestLocalProviderConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("local transport requires Linux")
	}

	server, schema := server(t)
	_, config := object(t, schema.Provider, map[string]any{"transport": "local"})
	response, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: config})
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)
}
