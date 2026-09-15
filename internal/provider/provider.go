// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	filepath "path"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/local"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/remote"
)

var _ provider.Provider = (*deploymentProvider)(nil)

type deploymentProvider struct {
	version string
}

type providerModel struct {
	Transport      types.String `tfsdk:"transport"`
	PodmanSocket   types.String `tfsdk:"podman_socket"`
	SystemdBus     types.String `tfsdk:"systemd_bus"`
	Host           types.String `tfsdk:"host"`
	Port           types.Int64  `tfsdk:"port"`
	User           types.String `tfsdk:"user"`
	PrivateKeyFile types.String `tfsdk:"private_key_file"`
	KnownHostsFile types.String `tfsdk:"known_hosts_file"`
	HostKey        types.String `tfsdk:"host_key"`
	Timeout        types.String `tfsdk:"timeout"`
}

func (p *deploymentProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "quadlet"
	resp.Version = p.version
}

func (p *deploymentProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Deploys Quadlets, native systemd user units and their configuration files over SSH or locally on Linux. Quadlets are validated with the host's generator. Podman secrets use the Podman API. Requires Terraform 1.11+ or OpenTofu 1.11+.",
		Attributes: map[string]schema.Attribute{
			"transport":     schema.StringAttribute{Optional: true, MarkdownDescription: "Host access: ssh (default) or local. Local requires Linux and runs as the provider process user; SSH arguments must be omitted."},
			"podman_socket": schema.StringAttribute{Optional: true, MarkdownDescription: "Absolute Podman API socket path on the target. Defaults to /run/user/<uid>/podman/podman.sock. Used only for Podman secrets."},
			"systemd_bus":   schema.StringAttribute{Optional: true, MarkdownDescription: "Absolute systemd user bus socket path on the target. Defaults to /run/user/<uid>/bus. Authentication uses the target user's UID."},
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "SSH hostname or IP address. Required with ssh transport. SSH configuration files are not evaluated.",
			},
			"port": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "SSH port. Defaults to 22.",
			},
			"user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "SSH user running rootless Podman. Required with ssh transport; omit with local transport.",
			},
			"private_key_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Private key path (supports ~/). Omit to use ssh-agent; encrypted keys must be loaded into the agent.",
			},
			"known_hosts_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Defaults to ~/.ssh/known_hosts. Unknown or changed keys are rejected.",
			},
			"host_key": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Pinned public host key in authorized_keys format; takes precedence over known_hosts_file.",
			},
			"timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Per-operation timeout as a Go duration, including connection retries. Defaults to 15m.",
			},
		},
	}
}

func (p *deploymentProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)

	if resp.Diagnostics.HasError() {
		return
	}

	for name, value := range map[string]interface{ IsUnknown() bool }{
		"transport": model.Transport, "podman_socket": model.PodmanSocket, "systemd_bus": model.SystemdBus,
		"host": model.Host, "port": model.Port, "user": model.User, "private_key_file": model.PrivateKeyFile,
		"known_hosts_file": model.KnownHostsFile, "host_key": model.HostKey, "timeout": model.Timeout,
	} {
		if value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider configuration", "the value must be known before the host can be reached")
		}
	}

	if resp.Diagnostics.HasError() {
		return
	}

	transport := model.Transport.ValueString()
	if model.Transport.IsNull() {
		transport = "ssh"
	}

	config := remote.Config{
		Host: model.Host.ValueString(), Port: 22, User: model.User.ValueString(),
		PrivateKeyFile: model.PrivateKeyFile.ValueString(),
		KnownHostsFile: "~/.ssh/known_hosts", HostKey: model.HostKey.ValueString(),
	}
	if !model.Port.IsNull() {
		config.Port = int(model.Port.ValueInt64())
	}

	if !model.KnownHostsFile.IsNull() {
		config.KnownHostsFile = model.KnownHostsFile.ValueString()
	}

	a := &access{timeout: 15 * time.Minute, podmanSocket: model.PodmanSocket.ValueString(), systemdBus: model.SystemdBus.ValueString()}
	for name, value := range map[string]types.String{"podman_socket": model.PodmanSocket, "systemd_bus": model.SystemdBus} {
		socket := value.ValueString()
		if !value.IsNull() && (!filepath.IsAbs(socket) || filepath.Clean(socket) != socket || socket == "/" || strings.ContainsAny(socket, "\x00\r\n")) {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid socket path", "expected a clean absolute path on the target host")
		}
	}

	switch transport {
	case "ssh":
		if strings.TrimSpace(config.Host) == "" {
			resp.Diagnostics.AddAttributeError(path.Root("host"), "Invalid host", "host must not be blank with ssh transport")
		}

		if config.Port < 1 || config.Port > 65535 {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Invalid port", fmt.Sprintf("port %d is out of range", config.Port))
		}

		if strings.TrimSpace(config.User) == "" {
			resp.Diagnostics.AddAttributeError(path.Root("user"), "Invalid user", "user is required with ssh transport and must not be blank")
		}

		a.open = func(ctx context.Context) (host.Session, error) { return remote.Connect(ctx, config) }

	case "local":
		if runtime.GOOS != "linux" {
			resp.Diagnostics.AddAttributeError(path.Root("transport"), "Unsupported local platform", "local transport requires Linux")
		}

		for name, value := range map[string]interface{ IsNull() bool }{
			"host": model.Host, "port": model.Port, "user": model.User, "private_key_file": model.PrivateKeyFile,
			"known_hosts_file": model.KnownHostsFile, "host_key": model.HostKey,
		} {
			if !value.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(name), "SSH argument with local transport", "omit SSH arguments when transport is local")
			}
		}

		a.open = func(ctx context.Context) (host.Session, error) { return local.Connect(ctx) }

	default:
		resp.Diagnostics.AddAttributeError(path.Root("transport"), "Invalid transport", "expected ssh or local")
	}

	if !model.Timeout.IsNull() {
		duration, err := time.ParseDuration(model.Timeout.ValueString())
		if err != nil || duration <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid timeout", "expected a positive Go duration, for example 15m")
		}

		a.timeout = duration
	}

	if resp.Diagnostics.HasError() {
		return
	}

	resp.ResourceData = a
}

func (p *deploymentProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &deploymentResource{} },
		func() resource.Resource { return &secretResource{} },
	}
}

func (p *deploymentProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &deploymentProvider{version: version}
	}
}
