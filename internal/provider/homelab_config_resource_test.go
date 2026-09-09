// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func protocolConfig(t *testing.T) (tfprotov6.ProviderServer, tftypes.Object, map[string]tftypes.Value) {
	t.Helper()
	server := providerserver.NewProtocol6(New("test")())()
	schema, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, schema.Diagnostics)

	resourceType, ok := schema.ResourceSchemas["homelab_config"].ValueType().(tftypes.Object)
	require.True(t, ok)
	values := map[string]tftypes.Value{}
	for name, typ := range resourceType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	for name, value := range map[string]string{"host": "example.test", "firewall": "table inet filter {}"} {
		values[name] = tftypes.NewValue(tftypes.String, value)
	}
	for _, name := range []string{"files", "groups", "secrets"} {
		values[name] = tftypes.NewValue(resourceType.AttributeTypes[name], map[string]tftypes.Value{})
	}
	values["units"] = tftypes.NewValue(resourceType.AttributeTypes["units"], []tftypes.Value{})

	return server, resourceType, values
}

func dynamic(t *testing.T, typ tftypes.Type, value any) *tfprotov6.DynamicValue {
	t.Helper()
	result, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, value))
	require.NoError(t, err)

	return &result
}

func TestHomelabConfigProtocolPlanAndDrift(t *testing.T) {
	server, typ, config := protocolConfig(t)
	request := &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "homelab_config",
		PriorState:       dynamic(t, typ, nil),
		ProposedNewState: dynamic(t, typ, config),
		Config:           dynamic(t, typ, config),
	}
	response, err := server.PlanResourceChange(t.Context(), request)
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)

	planned, err := response.PlannedState.Unmarshal(typ)
	require.NoError(t, err)
	var values map[string]tftypes.Value
	require.NoError(t, planned.As(&values))
	require.True(t, values["revision"].IsKnown())
	require.False(t, values["revision"].IsNull())
	require.Equal(t, tftypes.NewValue(tftypes.String, "/var/home/core"), values["home_dir"])
	require.Equal(t, tftypes.NewValue(tftypes.Number, 22), values["port"])
	require.Equal(t, tftypes.NewValue(tftypes.String, "core"), values["user"])
	require.Equal(t, tftypes.NewValue(tftypes.String, "15m"), values["timeout"])

	wanted := values["revision"]
	values["revision"] = tftypes.NewValue(tftypes.String, "drift:files-changed")
	values["id"] = tftypes.NewValue(tftypes.String, "existing")
	request.PriorState = dynamic(t, typ, values)
	request.ProposedNewState = dynamic(t, typ, values)
	response, err = server.PlanResourceChange(t.Context(), request)
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)

	planned, err = response.PlannedState.Unmarshal(typ)
	require.NoError(t, err)
	require.NoError(t, planned.As(&values))
	require.Equal(t, wanted, values["revision"])
	require.Equal(t, tftypes.NewValue(tftypes.String, "existing"), values["id"])
}

func TestHomelabConfigProtocolUnknownAndInvalidInput(t *testing.T) {
	server, typ, config := protocolConfig(t)
	config["files"] = tftypes.NewValue(typ.AttributeTypes["files"], tftypes.UnknownValue)
	response, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "homelab_config",
		PriorState:       dynamic(t, typ, nil),
		ProposedNewState: dynamic(t, typ, config),
		Config:           dynamic(t, typ, config),
	})
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)

	config["files"] = tftypes.NewValue(typ.AttributeTypes["files"], map[string]tftypes.Value{
		"../escape": tftypes.NewValue(tftypes.String, "content"),
	})
	validation, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: "homelab_config",
		Config:   dynamic(t, typ, config),
	})
	require.NoError(t, err)
	require.NotEmpty(t, validation.Diagnostics)
	require.Contains(t, validation.Diagnostics[0].Detail, "invalid managed path")
}

func TestHomelabConfigConnectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attribute string
		value     any
		wantErr   bool
	}{
		{name: "empty host", attribute: "host", value: "", wantErr: true},
		{name: "blank host", attribute: "host", value: " \t\r\n", wantErr: true},
		{name: "unicode blank host", attribute: "host", value: "\u0085\u00a0\u2003\u2028\v", wantErr: true},
		{name: "IPv6 host", attribute: "host", value: "::1"},
		{name: "unknown host", attribute: "host", value: tftypes.UnknownValue},
		{name: "empty user", attribute: "user", value: "", wantErr: true},
		{name: "unknown user", attribute: "user", value: tftypes.UnknownValue},
		{name: "default user", attribute: "user"},
		{name: "zero port", attribute: "port", value: 0, wantErr: true},
		{name: "oversized port", attribute: "port", value: 65536, wantErr: true},
		{name: "first port", attribute: "port", value: 1},
		{name: "last port", attribute: "port", value: 65535},
		{name: "unknown port", attribute: "port", value: tftypes.UnknownValue},
		{name: "default port", attribute: "port"},
		{name: "malformed timeout", attribute: "timeout", value: "forever", wantErr: true},
		{name: "empty timeout", attribute: "timeout", value: "", wantErr: true},
		{name: "zero timeout", attribute: "timeout", value: "0s", wantErr: true},
		{name: "negative timeout", attribute: "timeout", value: "-1m", wantErr: true},
		{name: "subnanosecond timeout", attribute: "timeout", value: "0.1ns", wantErr: true},
		{name: "compound timeout", attribute: "timeout", value: "1m30s"},
		{name: "unknown timeout", attribute: "timeout", value: tftypes.UnknownValue},
		{name: "default timeout", attribute: "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, typ, config := protocolConfig(t)
			config[tc.attribute] = tftypes.NewValue(typ.AttributeTypes[tc.attribute], tc.value)
			config["files"] = tftypes.NewValue(typ.AttributeTypes["files"], tftypes.UnknownValue)

			response, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{
				TypeName: "homelab_config",
				Config:   dynamic(t, typ, config),
			})
			require.NoError(t, err)
			if !tc.wantErr {
				require.Empty(t, response.Diagnostics)

				return
			}

			require.Len(t, response.Diagnostics, 1)
			diagnostic := response.Diagnostics[0]
			require.Equal(t, tfprotov6.DiagnosticSeverityError, diagnostic.Severity)
			require.Equal(t, tftypes.NewAttributePath().WithAttributeName(tc.attribute), diagnostic.Attribute)
		})
	}
}

func TestHomelabConfigWriteOnlyPlan(t *testing.T) {
	server, typ, config := protocolConfig(t)
	config["secrets"] = tftypes.NewValue(typ.AttributeTypes["secrets"], map[string]tftypes.Value{
		"app_password": tftypes.NewValue(tftypes.String, "source-reference"),
	})

	plan := func(value any) map[string]tftypes.Value {
		t.Helper()
		config["secret_values_wo"] = tftypes.NewValue(typ.AttributeTypes["secret_values_wo"], value)

		response, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
			TypeName:         "homelab_config",
			PriorState:       dynamic(t, typ, nil),
			ProposedNewState: dynamic(t, typ, config),
			Config:           dynamic(t, typ, config),
		})
		require.NoError(t, err)
		require.Empty(t, response.Diagnostics)
		require.Empty(t, response.PlannedPrivate)

		planned, err := response.PlannedState.Unmarshal(typ)
		require.NoError(t, err)
		var values map[string]tftypes.Value
		require.NoError(t, planned.As(&values))
		require.True(t, values["secret_values_wo"].IsNull())
		require.True(t, values["revision"].IsKnown())

		return values
	}

	first := plan(map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, "original-secret")})
	rotated := plan(map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, "rotated-secret")})
	unknown := plan(tftypes.UnknownValue)
	require.Equal(t, first["revision"], rotated["revision"], "secret bytes must not influence fingerprints")
	require.Equal(t, first["revision"], unknown["revision"], "unknown ephemeral values must not defer the revision")

	config["secrets_revision"] = tftypes.NewValue(tftypes.String, "2")
	bumped := plan(tftypes.UnknownValue)
	require.NotEqual(t, first["revision"], bumped["revision"])

	config["secrets"] = tftypes.NewValue(typ.AttributeTypes["secrets"], map[string]tftypes.Value{
		"app_password": tftypes.NewValue(tftypes.String, "replacement-reference"),
	})
	changedReference := plan(tftypes.UnknownValue)
	require.NotEqual(t, bumped["revision"], changedReference["revision"])
}

func TestHomelabConfigWriteOnlyValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  any
		wantErr bool
	}{
		{name: "known", values: map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, "private-value")}},
		{name: "unknown map", values: tftypes.UnknownValue},
		{name: "unknown value", values: map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)}},
		{name: "missing", wantErr: true},
		{name: "empty", values: map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, "")}, wantErr: true},
		{name: "null", values: map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, nil)}, wantErr: true},
		{name: "wrong key", values: map[string]tftypes.Value{"wrong_name": tftypes.NewValue(tftypes.String, "private-value")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, typ, config := protocolConfig(t)
			config["secrets"] = tftypes.NewValue(typ.AttributeTypes["secrets"], map[string]tftypes.Value{
				"app_password": tftypes.NewValue(tftypes.String, "source-reference"),
			})
			config["secret_values_wo"] = tftypes.NewValue(typ.AttributeTypes["secret_values_wo"], tc.values)

			response, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{
				TypeName: "homelab_config",
				Config:   dynamic(t, typ, config),
				ClientCapabilities: &tfprotov6.ValidateResourceConfigClientCapabilities{
					WriteOnlyAttributesAllowed: true,
				},
			})
			require.NoError(t, err)
			if tc.wantErr {
				require.NotEmpty(t, response.Diagnostics)
			} else {
				require.Empty(t, response.Diagnostics)
			}

			for _, diagnostic := range response.Diagnostics {
				require.NotContains(t, diagnostic.Detail+diagnostic.Summary, "private-value")
			}
		})
	}
}

func TestHomelabConfigApplyRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attribute string
		value     any
		detail    string
	}{
		{name: "missing secret", attribute: "secret_values_wo", detail: "exactly the names"},
		{
			name: "empty secret", attribute: "secret_values_wo", detail: "empty or missing value",
			value: map[string]tftypes.Value{"app_password": tftypes.NewValue(tftypes.String, "")},
		},
		{
			name: "wrong secret", attribute: "secret_values_wo", detail: "empty or missing value",
			value: map[string]tftypes.Value{"wrong_name": tftypes.NewValue(tftypes.String, "private-value")},
		},
		{
			name: "extra secret", attribute: "secret_values_wo", detail: "exactly the names",
			value: map[string]tftypes.Value{
				"app_password": tftypes.NewValue(tftypes.String, "private-value"),
				"extra":        tftypes.NewValue(tftypes.String, "private-value"),
			},
		},
		{
			name: "path traversal", attribute: "files", detail: "invalid managed path",
			value: map[string]tftypes.Value{"../escape": tftypes.NewValue(tftypes.String, "content")},
		},
		{
			name: "secret collision", attribute: "secrets", detail: "invalid or conflicting secret reference",
			value: map[string]tftypes.Value{
				"app_password": tftypes.NewValue(tftypes.String, "first"),
				"app-password": tftypes.NewValue(tftypes.String, "second"),
			},
		},
		{name: "empty firewall", attribute: "firewall", value: "", detail: "firewall must not be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, typ, config := protocolConfig(t)
			config["secrets"] = tftypes.NewValue(typ.AttributeTypes["secrets"], map[string]tftypes.Value{
				"app_password": tftypes.NewValue(tftypes.String, "reference"),
			})
			config["secret_values_wo"] = tftypes.NewValue(typ.AttributeTypes["secret_values_wo"], map[string]tftypes.Value{
				"app_password": tftypes.NewValue(tftypes.String, "private-value"),
			})
			config[tc.attribute] = tftypes.NewValue(typ.AttributeTypes[tc.attribute], tc.value)

			// Apply must reject values resolved after planning before host preparation.
			planned := maps.Clone(config)
			planned["secret_values_wo"] = tftypes.NewValue(typ.AttributeTypes["secret_values_wo"], nil)
			response, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
				TypeName:     "homelab_config",
				PriorState:   dynamic(t, typ, nil),
				PlannedState: dynamic(t, typ, planned),
				Config:       dynamic(t, typ, config),
			})
			require.NoError(t, err)
			require.Len(t, response.Diagnostics, 1)
			require.Contains(t, response.Diagnostics[0].Detail, tc.detail)
			require.NotContains(t, response.Diagnostics[0].Detail, "private-value")
		})
	}
}

func TestHomelabConfigLegacyStateAddsNullWriteOnlyAttribute(t *testing.T) {
	server, typ, _ := protocolConfig(t)
	response, err := server.UpgradeResourceState(t.Context(), &tfprotov6.UpgradeResourceStateRequest{
		TypeName: "homelab_config",
		Version:  0,
		RawState: &tfprotov6.RawState{JSON: []byte(`{
		 "id": "existing", "revision": "existing-revision",
		 "host": "192.0.2.10", "port": 22, "user": "core",
		 "private_key_file": null, "known_hosts_file": "~/.ssh/known_hosts", "host_key": null,
		 "home_dir": "/var/home/core", "firewall_path": "/etc/nftables/main.nft", "timeout": "15m",
		 "files": {}, "units": [], "groups": {}, "firewall": "table inet filter {}",
		 "secrets": {"app_password": "existing-reference"}, "secrets_revision": "1"
		}`)},
	})
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)

	upgraded, err := response.UpgradedState.Unmarshal(typ)
	require.NoError(t, err)
	var values map[string]tftypes.Value
	require.NoError(t, upgraded.As(&values))
	require.True(t, values["secret_values_wo"].IsNull())
	require.Equal(t, tftypes.NewValue(tftypes.String, "existing-revision"), values["revision"])
	require.Equal(t, tftypes.NewValue(typ.AttributeTypes["secrets"], map[string]tftypes.Value{
		"app_password": tftypes.NewValue(tftypes.String, "existing-reference"),
	}), values["secrets"])
}
