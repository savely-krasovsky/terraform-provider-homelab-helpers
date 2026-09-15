// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/rand"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/deployment"
)

var (
	_ resource.Resource                   = (*deploymentResource)(nil)
	_ resource.ResourceWithConfigure      = (*deploymentResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*deploymentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*deploymentResource)(nil)
	_ resource.ResourceWithImportState    = (*deploymentResource)(nil)
)

type deploymentAccess interface {
	engine(context.Context, bool, func(context.Context, deployment.Engine) error) error
}

type deploymentResource struct {
	access deploymentAccess
}

type deploymentModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	AppliedRevision types.String `tfsdk:"applied_revision"`
	Files           types.Map    `tfsdk:"files"`
	Units           types.Set    `tfsdk:"units"`
	Restart         types.Set    `tfsdk:"restart"`
	TryRestart      types.Set    `tfsdk:"try_restart"`
	Enable          types.Set    `tfsdk:"enable"`
	Triggers        types.Map    `tfsdk:"triggers"`
}

func (r *deploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (r *deploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A named deployment on a rootless Podman host: Quadlets, native user units and configuration files. Any file, trigger or activation policy change activates the whole deployment. One host record tracks ownership and the successfully applied revision. Files declared in configuration become managed, including existing files. Overlapping deployments are rejected. Systemd and Quadlet interpret drop-ins. File replacements are individually atomic; a failed activation is retried by a subsequent apply. Destroy stops recorded units and removes recorded files; application data, volumes and networks stay. Systemd dependencies and unit commands may affect other workloads and remain the author's responsibility.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "Deployment name, unique for the host user. Use 1–64 letters, digits, dots, underscores or hyphens, starting with a letter or digit. Changing it replaces the deployment.",
			},
			"applied_revision": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Revision committed after files and units were applied successfully. Empty while recovery is pending; the next plan schedules an update even if the files already match.",
			},
			"files": schema.MapAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Path relative to ~/.config to content. Configuration lands in the state; reference secrets instead of embedding them.",
			},
			"units": schema.SetAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Owned services, timers, sockets and targets, discovered from the native files and the host's Quadlet generator during apply. Includes passive network and volume units. Empty for a file-only deployment. Ownership does not imply activation.",
			},
			"restart": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Owned units to start or restart whenever any deployment file, trigger or activation policy changes. Defaults to no units. Must not overlap try_restart.",
			},
			"try_restart": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Owned units to restart only if active, on the same deployment-wide changes. Use for socket-activated services. Defaults to no units.",
			},
			"enable": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Owned native units to enable at boot. Defaults to no units; removing a previously enabled unit removes its boot links without stopping it. Quadlets use their own [Install] section.",
			},
			"triggers": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "External revisions, including secret installation revisions. Any change activates the deployment. Values are stored in state; never supply secret contents.",
			},
		},
	}
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	access, ok := req.ProviderData.(*access)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", "expected host access configured by the provider")

		return
	}

	r.access = access
}

func (m deploymentModel) payload(ctx context.Context) (deployment.Payload, diag.Diagnostics) {
	var payload deployment.Payload

	var diagnostics diag.Diagnostics
	diagnostics.Append(m.Files.ElementsAs(ctx, &payload.Files, false)...)

	for _, field := range []struct {
		value  types.Set
		target *[]string
	}{{m.Restart, &payload.Restart}, {m.TryRestart, &payload.TryRestart}, {m.Enable, &payload.Enable}} {
		if !field.value.IsNull() {
			diagnostics.Append(field.value.ElementsAs(ctx, field.target, false)...)
			slices.Sort(*field.target)
		}
	}

	if !m.Triggers.IsNull() {
		diagnostics.Append(m.Triggers.ElementsAs(ctx, &payload.Triggers, false)...)
	}

	return payload, diagnostics
}

func (m deploymentModel) known(ctx context.Context) bool {
	for _, a := range []attr.Value{m.Name, m.Files, m.Restart, m.TryRestart, m.Enable, m.Triggers} {
		value, err := a.ToTerraformValue(ctx)
		if err != nil || !value.IsFullyKnown() {
			return false
		}
	}

	return true
}

func (m deploymentModel) validate(ctx context.Context) diag.Diagnostics {
	payload, diagnostics := m.payload(ctx)
	if diagnostics.HasError() {
		return diagnostics
	}

	if err := payload.Validate(); err != nil {
		diagnostics.AddError("Invalid deployment description", err.Error())
	}

	return diagnostics
}

func (r *deploymentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model deploymentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if !model.Name.IsNull() && !model.Name.IsUnknown() {
		if err := deployment.ValidateName(model.Name.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid deployment name", err.Error())
		}
	}

	if !model.known(ctx) {
		return
	}

	resp.Diagnostics.Append(model.validate(ctx)...)
}

func (r *deploymentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var model deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if !req.State.Raw.IsNull() {
		var previousName types.String
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &previousName)...)

		if !previousName.Equal(model.Name) {
			model.ID = types.StringUnknown()
			model.Units = types.SetUnknown(types.StringType)
		}
	}

	if !model.known(ctx) {
		model.Units = types.SetUnknown(types.StringType)
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &model)...)

		return
	}

	resp.Diagnostics.Append(model.validate(ctx)...)

	if resp.Diagnostics.HasError() {
		return
	}

	payload, diagnostics := model.payload(ctx)
	resp.Diagnostics.Append(diagnostics...)

	if resp.Diagnostics.HasError() {
		return
	}

	revision := types.StringValue(deployment.Digest(payload))
	if !model.AppliedRevision.Equal(revision) {
		model.Units = types.SetUnknown(types.StringType)
	}

	model.AppliedRevision = revision
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &model)...)
}

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	model.ID = types.StringValue(rand.Text())
	claimed, diagnostics := r.apply(ctx, &model)
	resp.Diagnostics.Append(diagnostics...)
	// Preserve the owner after a partial or uncertain create so Terraform can
	// clean it up. A conflict rejected before claiming must not enter state.
	if claimed || !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *deploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	_, diagnostics := r.apply(ctx, &model)
	resp.Diagnostics.Append(diagnostics...)

	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *deploymentResource) apply(ctx context.Context, model *deploymentModel) (bool, diag.Diagnostics) {
	payload, diagnostics := model.payload(ctx)
	if diagnostics.HasError() {
		return false, diagnostics
	}

	diagnostics.Append(model.validate(ctx)...)

	if diagnostics.HasError() {
		return false, diagnostics
	}

	model.AppliedRevision = types.StringValue("")

	var result deployment.ApplyResult

	err := r.access.engine(ctx, true, func(ctx context.Context, engine deployment.Engine) (err error) {
		engine.Name, engine.ID = model.Name.ValueString(), model.ID.ValueString()
		result, err = engine.Apply(ctx, payload)

		return err
	})
	model.Units, _ = types.SetValueFrom(ctx, types.StringType, append([]string{}, result.Units...))

	if err != nil {
		diagnostics.AddError("Deployment failed", err.Error())
		return result.Claimed, diagnostics
	}

	model.AppliedRevision = types.StringValue(deployment.Digest(payload))

	return result.Claimed, diagnostics
}

// Read replaces the files in state with what the host holds, so the next plan
// shows every drifted or missing file.
func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var snapshot deployment.Snapshot

	err := r.access.engine(ctx, false, func(ctx context.Context, engine deployment.Engine) (err error) {
		engine.Name, engine.ID = model.Name.ValueString(), model.ID.ValueString()
		snapshot, err = engine.Read(ctx)

		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot refresh deployment", err.Error())

		return
	}

	if !snapshot.Found {
		resp.State.RemoveResource(ctx)

		return
	}

	values, diagnostics := types.MapValueFrom(ctx, types.StringType, snapshot.Files)
	resp.Diagnostics.Append(diagnostics...)

	if resp.Diagnostics.HasError() {
		return
	}

	model.Units, diagnostics = types.SetValueFrom(ctx, types.StringType, append([]string{}, snapshot.Units...))
	resp.Diagnostics.Append(diagnostics...)

	model.Files = values
	model.AppliedRevision = types.StringValue(snapshot.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.access.engine(ctx, true, func(ctx context.Context, engine deployment.Engine) error {
		engine.Name, engine.ID = model.Name.ValueString(), model.ID.ValueString()
		return engine.Delete(ctx)
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot remove deployment", err.Error())
	}
}

func (r *deploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := deployment.ValidateName(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid deployment name", err.Error())
		return
	}

	var (
		id       string
		snapshot deployment.Snapshot
	)

	err := r.access.engine(ctx, false, func(ctx context.Context, engine deployment.Engine) (err error) {
		engine.Name = req.ID
		id, snapshot, err = engine.Lookup(ctx)

		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot import deployment", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("files"), snapshot.Files)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("units"), append([]string{}, snapshot.Units...))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("applied_revision"), snapshot.Revision)...)
}
