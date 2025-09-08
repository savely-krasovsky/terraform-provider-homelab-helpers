// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Ensure HomelabHelpers satisfies various provider interfaces.
var _ provider.Provider = &HomelabHelpers{}
var _ provider.ProviderWithFunctions = &HomelabHelpers{}

// HomelabHelpers defines the provider implementation.
type HomelabHelpers struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

func (p *HomelabHelpers) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "homelab-helpers"
	resp.Version = p.version
}

func (p *HomelabHelpers) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{},
	}
}

func (p *HomelabHelpers) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
}

func (p *HomelabHelpers) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *HomelabHelpers) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *HomelabHelpers) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{
		NewDirSetFunction,
		NewDirHashFunction,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &HomelabHelpers{
			version: version,
		}
	}
}
