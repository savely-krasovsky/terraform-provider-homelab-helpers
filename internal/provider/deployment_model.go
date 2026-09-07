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

type deploymentModel struct {
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

func (m deploymentModel) known(ctx context.Context) bool {
	for _, a := range []attr.Value{m.Files, m.Units, m.Groups, m.Firewall, m.Secrets, m.SecretsRevision} {
		value, err := a.ToTerraformValue(ctx)
		if err != nil || !value.IsFullyKnown() {
			return false
		}
	}

	return true
}

func (m deploymentModel) payload(ctx context.Context) (deployment.Payload, diag.Diagnostics) {
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

func (m deploymentModel) timeout() (time.Duration, error) {
	duration, err := time.ParseDuration(m.Timeout.ValueString())
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("timeout must be a positive Go duration, for example 15m")
	}

	return duration, nil
}
