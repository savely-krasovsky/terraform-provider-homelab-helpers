// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func (r *homelabConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Applies a rootless Podman/Quadlet deployment over verified SSH and SFTP. Secret values are supplied through a write-only argument and never stored in plan or state. Requires Terraform 1.11+ or OpenTofu 1.11+. Destroy stops managed units and removes owned configuration files; application data, firewall and credentials are retained.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable deployment identity derived from host, port, user and home directory.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"revision": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Desired configuration fingerprint. Refresh marks drift here when managed files, enabled timers, secrets or the journal differ.",
			},
			"host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "SSH hostname or IP address. SSH config files are not evaluated.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`[^\s\p{Z}\x85\x0b]`), "must contain a non-whitespace character"),
				},
			},
			"port": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:             int64default.StaticInt64(22),
				Validators:          []validator.Int64{int64validator.Between(1, 65535)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				MarkdownDescription: "SSH port.",
			},
			"user": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("core"),
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "SSH user running rootless Podman. Requires passwordless sudo for host configuration.",
			},
			"private_key_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Local private key path (supports ~/). Omit to use ssh-agent. Only the path enters state.",
			},
			"known_hosts_file": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("~/.ssh/known_hosts"),
				MarkdownDescription: "Local known_hosts file. The host must already be trusted, unless host_key is configured.",
			},
			"host_key": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Pinned SSH public host key in authorized_keys format. Takes precedence over known_hosts_file.",
			},
			"home_dir": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("/var/home/core"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "Absolute remote home. Configuration is managed below .config and the journal below .local/state/homelab. Symlink components are rejected.",
			},
			"firewall_path": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("/etc/nftables/main.nft"),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "Persistent nftables file. Retained on destroy to preserve host connectivity and protection.",
			},
			"timeout": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("15m"),
				MarkdownDescription: "Timeout for each lifecycle operation, including connection retries and the deployment lock (Go duration syntax).",
			},
			"files": schema.MapAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Relative path to rendered content under home_dir/.config. Do not include plaintext secrets: configuration values are persisted in state.",
			},
			"units": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Owned systemd user services, timers and sockets. Generated Quadlet units must be listed by generated unit name.",
			},
			"groups": schema.MapNestedAttribute{
				Required:            true,
				MarkdownDescription: "Restart groups. Put a pod and every member in the same group; hash must cover definitions and mounted configuration.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"units": schema.ListAttribute{
							Required: true, ElementType: types.StringType,
							MarkdownDescription: "Units restarted together in one systemd transaction.",
						},
						"enable": schema.ListAttribute{
							Required: true, ElementType: types.StringType,
							MarkdownDescription: "Native units to enable (normally timers). Do not enable generated Quadlets.",
						},
						"hash": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Fingerprint of all configuration affecting the group.",
						},
						"uses_secrets": schema.BoolAttribute{
							Required:            true,
							MarkdownDescription: "Restart this group after importing changed secret references or a new secrets_revision.",
						},
					},
				},
			},
			"firewall": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Complete nftables ruleset, checked before any live change and applied atomically by nft.",
			},
			"secrets": schema.MapAttribute{
				Required: true, ElementType: types.StringType,
				MarkdownDescription: "Secret name to non-secret source reference or version. These values are stored in state and changes trigger reinstallation. The provider does not resolve references; pass the corresponding values in secret_values_wo. restic_* names become system credentials; other names become Podman secrets with underscores replaced by hyphens.",
			},
			"secret_values_wo": schema.MapAttribute{
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Secret name to plaintext value, preferably supplied by an ephemeral resource. Required for every entry in secrets, with no extra keys or empty values. Values are supplied through configuration and used during create/update. They are excluded from plan, state, fingerprints and journals.",
			},
			"secrets_revision": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("1"),
				MarkdownDescription: "Bump to install rotated values and restart consumers. A write-only value change alone cannot trigger an update. Refresh detects missing secrets without reading their values.",
			},
		},
	}
}
