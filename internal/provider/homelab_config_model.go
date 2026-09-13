// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/deployment"
)

type homelabConfigModel struct {
	ID              types.String `tfsdk:"id"`
	Revision        types.String `tfsdk:"revision"`
	Host            types.String `tfsdk:"host"`
	Port            types.Int64  `tfsdk:"port"`
	User            types.String `tfsdk:"user"`
	PrivateKeyFile  types.String `tfsdk:"private_key_file"`
	KnownHostsFile  types.String `tfsdk:"known_hosts_file"`
	HostKey         types.String `tfsdk:"host_key"`
	HomeDir         types.String `tfsdk:"home_dir"`
	FirewallPath    types.String `tfsdk:"firewall_path"`
	Timeout         types.String `tfsdk:"timeout"`
	Files           types.Map    `tfsdk:"files"`
	Units           types.List   `tfsdk:"units"`
	Groups          types.Map    `tfsdk:"groups"`
	DataRoot        types.String `tfsdk:"data_root"`
	Firewall        types.String `tfsdk:"firewall"`
	Secrets         types.Map    `tfsdk:"secrets"`
	SecretValuesWO  types.Map    `tfsdk:"secret_values_wo"`
	SecretsRevision types.String `tfsdk:"secrets_revision"`
}

type groupModel struct {
	Units       types.List   `tfsdk:"units"`
	Enable      types.List   `tfsdk:"enable"`
	Hash        types.String `tfsdk:"hash"`
	UsesSecrets types.Bool   `tfsdk:"uses_secrets"`
}

var groupType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"units":        types.ListType{ElemType: types.StringType},
	"enable":       types.ListType{ElemType: types.StringType},
	"hash":         types.StringType,
	"uses_secrets": types.BoolType,
}}

// known reports whether the configured input is settled. Units and groups are
// derived from it, so they are deliberately not part of the check.
func (m homelabConfigModel) known(ctx context.Context) bool {
	for _, a := range []attr.Value{m.Files, m.DataRoot, m.Firewall, m.Secrets, m.SecretsRevision} {
		value, err := a.ToTerraformValue(ctx)
		if err != nil || !value.IsFullyKnown() {
			return false
		}
	}

	return true
}

// derive fills the units and restart groups the deployment owns. Quadlet naming
// and the set of files that reach a group are provider knowledge, so the caller
// supplies only the rendered tree.
func (m *homelabConfigModel) derive(ctx context.Context) diag.Diagnostics {
	var (
		files       map[string]string
		diagnostics diag.Diagnostics
	)

	diagnostics.Append(m.Files.ElementsAs(ctx, &files, false)...)
	if diagnostics.HasError() {
		return diagnostics
	}

	units, groups, err := deployment.Derive(files)
	if err != nil {
		diagnostics.AddError("Invalid configuration tree", err.Error())

		return diagnostics
	}

	models := make(map[string]groupModel, len(groups))
	for name, group := range groups {
		unitValues, unitDiagnostics := types.ListValueFrom(ctx, types.StringType, group.Units)
		enableValues, enableDiagnostics := types.ListValueFrom(ctx, types.StringType, group.Enable)
		diagnostics.Append(unitDiagnostics...)
		diagnostics.Append(enableDiagnostics...)

		models[name] = groupModel{
			Units:       unitValues,
			Enable:      enableValues,
			Hash:        types.StringValue(group.Hash),
			UsesSecrets: types.BoolValue(group.UsesSecrets),
		}
	}

	unitValues, unitDiagnostics := types.ListValueFrom(ctx, types.StringType, units)
	groupValues, groupDiagnostics := types.MapValueFrom(ctx, groupType, models)
	diagnostics.Append(unitDiagnostics...)
	diagnostics.Append(groupDiagnostics...)
	if diagnostics.HasError() {
		return diagnostics
	}

	m.Units = unitValues
	m.Groups = groupValues

	return diagnostics
}

func (m homelabConfigModel) payload(ctx context.Context) (deployment.Payload, diag.Diagnostics) {
	var (
		payload     deployment.Payload
		groups      map[string]groupModel
		diagnostics diag.Diagnostics
	)

	diagnostics.Append(m.Files.ElementsAs(ctx, &payload.Files, false)...)
	diagnostics.Append(m.Units.ElementsAs(ctx, &payload.Units, false)...)
	diagnostics.Append(m.Secrets.ElementsAs(ctx, &payload.Secrets, false)...)
	diagnostics.Append(m.Groups.ElementsAs(ctx, &groups, false)...)
	payload.Groups = make(map[string]deployment.Group, len(groups))
	payload.Firewall = m.Firewall.ValueString()
	payload.DataRoot = m.DataRoot.ValueString()

	for name, group := range groups {
		value := deployment.Group{
			Hash:        group.Hash.ValueString(),
			UsesSecrets: group.UsesSecrets.ValueBool(),
		}
		diagnostics.Append(group.Units.ElementsAs(ctx, &value.Units, false)...)
		diagnostics.Append(group.Enable.ElementsAs(ctx, &value.Enable, false)...)
		payload.Groups[name] = value
	}

	payload.SecretsRevision = deployment.Digest(struct {
		References map[string]string
		Revision   string
	}{payload.Secrets, m.SecretsRevision.ValueString()})

	return payload, diagnostics
}

func (m homelabConfigModel) timeout() (time.Duration, error) {
	duration, err := time.ParseDuration(m.Timeout.ValueString())
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("timeout must be a positive Go duration, for example 15m")
	}

	return duration, nil
}
