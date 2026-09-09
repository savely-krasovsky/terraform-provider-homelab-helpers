// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
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

var (
	_ provider.Provider              = (*homelabHelpers)(nil)
	_ provider.ProviderWithFunctions = (*homelabHelpers)(nil)
)

type homelabHelpers struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

func (p *homelabHelpers) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "homelab"
	resp.Version = p.version
}

func (p *homelabHelpers) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Helpers for directory discovery and declarative FCOS deployment over SSH. Requires Terraform 1.8+ or OpenTofu 1.8+. See the deployment resource for host prerequisites and secret handling.",
		Attributes:          map[string]schema.Attribute{},
	}
}

func (p *homelabHelpers) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *homelabHelpers) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &homelabConfigResource{} },
	}
}

func (p *homelabHelpers) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func (p *homelabHelpers) Functions(context.Context) []func() function.Function {
	return []func() function.Function{
		func() function.Function { return dirSetFunction{} },
		func() function.Function { return dirHashFunction{} },
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &homelabHelpers{
			version: version,
		}
	}
}
