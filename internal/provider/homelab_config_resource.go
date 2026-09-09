// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"path"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/deployment"
	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

var (
	_ resource.Resource                   = (*homelabConfigResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*homelabConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*homelabConfigResource)(nil)
)

type homelabConfigResource struct{}

func (r *homelabConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (r *homelabConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model homelabConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !model.Timeout.IsNull() && !model.Timeout.IsUnknown() {
		if _, err := model.timeout(); err != nil {
			resp.Diagnostics.AddAttributeError(tfpath.Root("timeout"), "Invalid timeout", err.Error())
		}
	}

	if resp.Diagnostics.HasError() || !model.known(ctx) {
		return
	}

	payload, diagnostics := model.payload(ctx)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := payload.Validate(); err != nil {
		resp.Diagnostics.AddError("Invalid deployment", err.Error())

		return
	}

	value, err := model.SecretValuesWO.ToTerraformValue(ctx)
	if err == nil && value.IsFullyKnown() {
		values, diagnostics := readSecretValues(ctx, req.Config, payload.Secrets)
		clear(values)
		resp.Diagnostics.Append(diagnostics...)
	}
}

func (r *homelabConfigResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var model homelabConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || !model.known(ctx) {
		return
	}

	payload, diagnostics := model.payload(ctx)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	model.Revision = types.StringValue(deployment.Digest(payload))
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &model)...)
}

func (r *homelabConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model homelabConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.apply(ctx, &model, req.Config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *homelabConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model homelabConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.apply(ctx, &model, req.Config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *homelabConfigResource) apply(ctx context.Context, model *homelabConfigModel, config tfsdk.Config) diag.Diagnostics {
	payload, diagnostics := model.payload(ctx)
	if diagnostics.HasError() {
		return diagnostics
	}
	if err := payload.Validate(); err != nil {
		diagnostics.AddError("Invalid deployment", err.Error())

		return diagnostics
	}

	values, valueDiagnostics := readSecretValues(ctx, config, payload.Secrets)
	diagnostics.Append(valueDiagnostics...)
	if diagnostics.HasError() {
		return diagnostics
	}
	defer clear(values)

	err := r.withHost(ctx, *model, true, func(ctx context.Context, engine deployment.Engine) error {
		return engine.Apply(ctx, payload, values)
	})
	if err != nil {
		diagnostics.AddError("Deployment failed", err.Error())

		return diagnostics
	}

	model.ID = types.StringValue(deployment.Digest([]string{
		model.Host.ValueString(), model.Port.String(), model.User.ValueString(), model.HomeDir.ValueString(),
	}))
	model.Revision = types.StringValue(deployment.Digest(payload))

	return diagnostics
}

func (r *homelabConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model homelabConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	payload, diagnostics := model.payload(ctx)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.withHost(ctx, model, false, func(ctx context.Context, engine deployment.Engine) error {
		revision, err := engine.Read(ctx, payload)
		model.Revision = types.StringValue(revision)

		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		model.Revision = types.StringValue("drift:missing-state-directory")
	} else if err != nil {
		resp.Diagnostics.AddError("Cannot refresh deployment", err.Error())

		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *homelabConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model homelabConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	payload, diagnostics := model.payload(ctx)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.withHost(ctx, model, true, func(ctx context.Context, engine deployment.Engine) error {
		return engine.Delete(ctx, deployment.Ownership{
			Files: slices.Collect(maps.Keys(payload.Files)),
			Units: payload.Units,
		})
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot remove deployment", err.Error())
	}
}

func (r *homelabConfigResource) withHost(
	ctx context.Context,
	model homelabConfigModel,
	prepare bool,
	operation func(context.Context, deployment.Engine) error,
) error {
	paths := deployment.Paths{
		Home:     model.HomeDir.ValueString(),
		Firewall: model.FirewallPath.ValueString(),
	}
	if err := paths.Validate(); err != nil {
		return err
	}

	duration, err := model.timeout()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	client, err := remote.Connect(ctx, remote.Config{
		Host:           model.Host.ValueString(),
		Port:           int(model.Port.ValueInt64()),
		User:           model.User.ValueString(),
		PrivateKeyFile: model.PrivateKeyFile.ValueString(),
		KnownHostsFile: model.KnownHostsFile.ValueString(),
		HostKey:        model.HostKey.ValueString(),
	})
	if err != nil {
		return err
	}
	defer client.Close()

	if prepare {
		if err := deployment.Prepare(ctx, client, paths); err != nil {
			return err
		}
	} else if _, err := client.Stat(paths.State()); err != nil {
		return err
	}

	ctx, unlock, err := client.Lock(ctx, path.Join(paths.State(), "deploy.lock"))
	if err != nil {
		return err
	}
	defer unlock()

	return operation(ctx, deployment.Engine{
		Host:  client,
		Paths: paths,
	})
}
