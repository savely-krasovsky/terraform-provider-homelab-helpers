# Migration to 0.1.0

## Scaffolding baseline

The project follows
[terraform-provider-scaffolding-framework at ae0e7c85859bf23055354246bb9b75f5686d8558](https://github.com/hashicorp/terraform-provider-scaffolding-framework/tree/ae0e7c85859bf23055354246bb9b75f5686d8558),
committed on 2026-09-01.

The test workflow keeps the template's three jobs: build, generate and test.
Terraform 1.13/1.14 form the acceptance matrix; OpenTofu runs as an additional
step. Build and test commands use the Makefile with CGO disabled.
The template's golangci-lint action remains part of the build job.

The release workflow keeps one GoReleaser job on Ubuntu. Go cross-compiles
Linux and Darwin amd64/arm64 without CGO. GoReleaser performs
archiving, registry manifest naming, checksumming, signing and publishing itself.
There are no artifact-transfer jobs, manual ZIP commands or packaging programs.

Go 1.27 is retained as requested. Framework 1.19.0, plugin-go 0.31.0 and
plugin-testing 1.16.0 already match the template. The tools module contains
copywrite and tfplugindocs, registered with Go tool directives. Removing its
obsolete koanf v1 dependency fixes a compilation conflict with copywrite's
koanf v2 dependency. golangci-lint is installed separately, as in the template.

The template's depguard rules and PR template are included. The deployment
resource and existing functions are registered; unused optional action and
ephemeral interfaces, sample resources and an HTTP client are not exposed.

## Existing files

No file present in HEAD is deleted by this migration. Issue-comment triage,
CODEOWNERS, the license and other existing repository metadata are retained.
The implementations of main.go, dirhash and dirset are preserved. Files with
pre-existing HashiCorp notices retain those notices alongside local attribution.

Copywrite remains inside make generate with Savely Krasovsky configured as the
copyright holder. Mixed-attribution files and LICENSE are maintained manually
and excluded from the generator: copywrite would otherwise replace the original
HashiCorp notices. Local IDE files and build output are also excluded.

Function tests use isolated temporary fixtures; CLI checks live in acceptance
tests. The old TestMain did not lose the exit code: Go's test wrapper already
propagates the result of m.Run. Changing that setup is about fixture isolation,
not fixing exit-code propagation.

## State and compatibility

This project already used Terraform Plugin Framework and plugin protocol v6.
HashiCorp's [SDKv2 migration workflow](https://developer.hashicorp.com/terraform/plugin/framework/migrating/workflow)
is therefore not applicable. The old provider exposed only functions, with no
resource state to upgrade. The provider source address and function signatures
stay unchanged; dirhash retains its ZIP-based hash format.

Future incompatible deployment schema changes must use a schema version and
[resource state upgrade](https://developer.hashicorp.com/terraform/plugin/framework/resources/state-upgrade).
Do not use state replace-provider for this update: the source address is the same.
The template's ID-only import example is not applicable either: an ID cannot
recover the SSH connection settings or desired configuration. The resource
instead adopts the existing host manifest during Create.

## Local development before publishing

Version 0.1.0 is unreleased until a maintainer tags and publishes it. To try the
source locally without changing the global Terraform/OpenTofu configuration:

```sh
make -C ../terraform-provider-homelab-helpers build
```

In the consuming repository create an ignored `.terraformrc` (use an absolute
path for the binary directory):

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/savely-krasovsky/homelab-helpers" = "/absolute/path/terraform-provider-homelab-helpers/bin"
  }
  direct {}
}
```

Then set `TF_CLI_CONFIG_FILE="$PWD/.terraformrc"` for `tofu validate`, `tofu plan`
and `tofu apply`. Development overrides bypass the selected registry binary for
those operations. Keep the existing initialized dependencies; `tofu init` still
tries to resolve published provider versions and cannot install unreleased
0.1.0. After publishing, remove the override and run
`tofu init -upgrade`, then review the updated lock file and plan.

## Ephemeral secret inputs

Supply secret values through `secret_values_wo`, using an ephemeral resource
or variable. Keep non-secret source references in `secrets` and the rotation
trigger in `secrets_revision`. The two maps must have the same keys, and all
secret values must be nonempty. Resolving values and authenticating to their
source belong to the calling configuration.

Adding `secret_values_wo` preserves existing state and host manifest
fingerprints. Write-only arguments require Terraform/OpenTofu 1.11+.
The provider reads these values from configuration during Create/Update;
refresh and destroy do not require them.

Plain `data` sources store their own results in state even when the receiving
argument is write-only. Use ephemeral inputs to keep values out of plan and
state throughout the configuration.

## Homelab provisioners

Replace `null_resource.fcos_provision_secrets` and `null_resource.sync_configs`
with one `homelab-helpers_deployment`, passing rendered files, restart groups,
owned unit names, firewall content, non-secret source references in `secrets`
and corresponding ephemeral values in `secret_values_wo`. Keep the dependency
on the FCOS VM. Do not use a `moved` block: null_resource state is not deployment
state and contains no owned-file manifest.

The new resource adopts the existing host journal and manifest at
`~/.local/state/homelab`. It understands both the Bash and Go CLI formats. The
first apply imports secrets and restarts groups as needed before writing its new
fingerprint. Existing parent directories left root-owned by old Ignition configs
are repaired individually. Application data and unrelated files are not adopted.

The two old null resources have no destroy provisioners. Removing them from
configuration only removes their Terraform state entries. Their old uploaded
CLI and payload files are harmless leftovers; they are not executed by the new
resource and can be removed separately after verifying migration.

Review the first plan: expect deletion of those two bookkeeping resources and
creation of one deployment resource. Unexpected VM replacement should be
investigated before applying. Apply is performed by the operator.

## Rotation and recovery

Bump `secrets_revision` for a value rotation with unchanged source references. Avoid
`-replace` for ordinary rotation: replacement runs Delete, stopping services and
removing managed configuration before Create.

On an apply failure keep the host manifest and pending journal, correct the
reported cause and retry. Refresh detects incomplete deployment. An unreachable
host is reported as an error, never silently removed from state. If the VM has
been destroyed outside Terraform, recover that infrastructure first or explicitly
remove the stale deployment state entry as part of the host replacement.
