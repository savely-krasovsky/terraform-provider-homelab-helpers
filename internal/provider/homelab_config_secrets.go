// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/deployment"
)

func readSecretValues(ctx context.Context, config tfsdk.Config, refs map[string]string) (map[string]string, diag.Diagnostics) {
	var values map[string]string

	diagnostics := config.GetAttribute(ctx, path.Root("secret_values_wo"), &values)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	if err := deployment.ValidateSecretValues(refs, values); err != nil {
		diagnostics.AddAttributeError(path.Root("secret_values_wo"), "Invalid secret values", err.Error())
		clear(values)

		return nil, diagnostics
	}

	return values, diagnostics
}
