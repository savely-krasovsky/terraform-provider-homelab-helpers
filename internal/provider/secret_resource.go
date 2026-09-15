// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/podman"
)

var (
	_ resource.Resource                   = (*secretResource)(nil)
	_ resource.ResourceWithConfigure      = (*secretResource)(nil)
	_ resource.ResourceWithValidateConfig = (*secretResource)(nil)
	_ resource.ResourceWithImportState    = (*secretResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*secretResource)(nil)
)

var secretName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type secretResource struct {
	access secretAccess
}

type secretModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	ValueWO  types.String `tfsdk:"value_wo"`
	Version  types.String `tfsdk:"version"`
	Revision types.String `tfsdk:"revision"`
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_podman_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A write-only Podman secret belonging to this resource. Existing secrets belonging to another owner are never replaced. Bump `version` to rotate the value; reference `revision` in deployment triggers to activate consumers. Rotation removes the owned object by ID and creates its replacement, so an interrupted rotation can leave the name absent until the next apply. Import by name recovers secrets created by this provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Opaque owner token stored in state and on the Podman secret. Stable across rotations.",
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value_wo": schema.StringAttribute{
				Required:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "The value, preferably from an ephemeral resource. Never stored in plan or state.",
			},
			"version": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("1"),
				MarkdownDescription: "Changing it reinstalls the value; a write-only value changing on its own is invisible to the plan.",
			},
			"revision": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Podman ID of the installed secret. Changes on rotation or recreation, including recreation with the same version; remains stable while that object exists. Reference it in deployment triggers. It is not derived from the secret value.",
			},
		},
	}
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	access, ok := req.ProviderData.(secretAccess)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", "expected host access configured by the provider")

		return
	}

	r.access = access
}

func (r *secretResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if !model.Name.IsUnknown() && !model.Name.IsNull() {
		if !secretName.MatchString(model.Name.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid name", "use letters, digits, dots, underscores or hyphens, starting with a letter or digit")
		}
	}

	if !model.ValueWO.IsUnknown() && !model.ValueWO.IsNull() && model.ValueWO.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("value_wo"), "Empty value", "value_wo must not be empty")
	}
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	model.ID = types.StringValue(rand.Text())
	claimed, diagnostics := r.install(ctx, req.Config, &model)
	resp.Diagnostics.Append(diagnostics...)

	// A request may have succeeded before the connection was lost. Keep the
	// owner so a subsequent destroy can remove only our object.
	if claimed || !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *secretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var previous, next types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &previous)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("name"), &next)...)

	if !resp.Diagnostics.HasError() && !previous.Equal(next) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	_, diagnostics := r.install(ctx, req.Config, &model)
	resp.Diagnostics.Append(diagnostics...)

	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

// install reads the write-only value from the configuration, the only place it exists.
func (r *secretResource) install(ctx context.Context, config tfsdk.Config, model *secretModel) (bool, diag.Diagnostics) {
	var value string

	diagnostics := config.GetAttribute(ctx, path.Root("value_wo"), &value)
	if diagnostics.HasError() {
		return false, diagnostics
	}

	if value == "" {
		diagnostics.AddAttributeError(path.Root("value_wo"), "Empty value", "value_wo must not be empty")

		return false, diagnostics
	}

	name := model.Name.ValueString()
	owner := model.ID.ValueString()
	claimed := false
	model.Revision = types.StringValue("")

	err := r.access.secrets(ctx, func(ctx context.Context, client secretClient) error {
		secret, err := client.Inspect(ctx, name)
		if err != nil {
			return err
		}

		if secret != nil {
			if secret.Spec.Labels[podman.OwnerLabel] != owner {
				return fmt.Errorf("secret %q already exists and belongs to another owner", name)
			}

			// Deleting the inspected ID cannot remove a concurrent replacement.
			if err := client.Remove(ctx, secret.ID); err != nil {
				return err
			}
		}

		claimed = true

		id, err := client.Create(ctx, name, owner, model.Version.ValueString(), value)
		if err == nil {
			model.Revision = types.StringValue(id)
		}

		return err
	})
	if err != nil {
		diagnostics.AddError("Cannot install secret", fmt.Sprintf("%s: %s", name, err))
	}

	return claimed, diagnostics
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var secret *podman.Secret

	err := r.access.secrets(ctx, func(ctx context.Context, client secretClient) (err error) {
		secret, err = client.Inspect(ctx, model.Name.ValueString())
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot refresh secret", err.Error())

		return
	}

	if secret == nil || secret.Spec.Labels[podman.OwnerLabel] != model.ID.ValueString() {
		resp.State.RemoveResource(ctx)
		return
	}

	model.Revision = types.StringValue(secret.ID)
	if version, present := secret.Spec.Labels[podman.VersionLabel]; present {
		model.Version = types.StringValue(version)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.access.secrets(ctx, func(ctx context.Context, client secretClient) error {
		secret, err := client.Inspect(ctx, model.Name.ValueString())
		if err != nil || secret == nil {
			return err
		}

		if secret.Spec.Labels[podman.OwnerLabel] != model.ID.ValueString() {
			return nil
		}

		return client.Remove(ctx, secret.ID)
	}); err != nil {
		resp.Diagnostics.AddError("Cannot remove secret", err.Error())
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !secretName.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid secret name", "import requires the full Podman secret name")
		return
	}

	var secret *podman.Secret

	err := r.access.secrets(ctx, func(ctx context.Context, client secretClient) (err error) {
		secret, err = client.Inspect(ctx, req.ID)
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot import secret", err.Error())
		return
	}

	if secret == nil || secret.Spec.Labels[podman.OwnerLabel] == "" {
		resp.Diagnostics.AddError("Cannot import secret", "no secret managed by this provider exists at that name")
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), secret.Spec.Labels[podman.OwnerLabel])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("revision"), secret.ID)...)

	if version, present := secret.Spec.Labels[podman.VersionLabel]; present {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), version)...)
	}
}
