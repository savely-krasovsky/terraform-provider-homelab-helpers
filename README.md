# Homelab Helpers Terraform Provider

Utilities and a declarative deployment resource for my Fedora CoreOS homelab.
Uses Terraform Plugin Framework and plugin protocol v6. The project follows the
[HashiCorp scaffolding baseline](https://github.com/hashicorp/terraform-provider-scaffolding-framework/tree/ae0e7c85859bf23055354246bb9b75f5686d8558), with
pure Go builds for Linux and macOS.

- `dirset(path, pattern)` lists directories matching a doublestar glob.
- `dirhash(path, pattern)` hashes matching file names and contents using the
  ZIP-based format.
- [`homelab-helpers_deployment`](docs/resources/deployment.md) applies rootless
  Podman/Quadlet configuration over verified SSH and SFTP.

Functions require Terraform/OpenTofu 1.8+; deployment requires 1.11+ for
write-only arguments. The provider runs on the apply machine;
no provider binary, Go runtime or SDK library is installed on FCOS.

## Deployment lifecycle

The resource stages the complete configuration on the host and checks it with
that host's Quadlet generator and `nft --check`. Before changing live files or
secrets it records ownership in a pending journal. It then installs changed
secrets supplied by the caller, atomically replaces files, reloads the systemd
user manager and restarts affected groups in one transaction. Put a pod and all its containers
in the same group. Include mounted configuration in the group's `hash`.

Refresh reads actual configuration and firewall fingerprints, checks native unit
enablement, the journal and secret existence. Drift produces an update in the
next plan. It does not poll container health or compare the live kernel firewall
ruleset; a deployment reapplies the validated persistent ruleset.

Pass secret values through `secret_values_wo`, preferably from an ephemeral
resource or variable. The caller resolves values from their source; the provider
installs them on the target host.
Write-only values are read from configuration during Create/Update, never from
plan or state. They are excluded from configuration fingerprints and journals.
Configuration in `files` and `firewall` does enter state: use secret references
instead of embedding secret values there.

The `secrets` map holds non-secret source references or versions keyed by
destination name. Supply exactly the same keys in `secret_values_wo`, with
nonempty values. `restic_*` names are installed in
`/etc/credstore` with underscores normalized to hyphens and permissions `0600`;
other names become Podman secrets. Bump `secrets_revision` to install rotated
values without changing their references. A write-only value change alone does
not trigger an update. Changed references and missing secrets also trigger
installation and consumer restarts. Removed references are retained on the host,
since backup services can still use them. Remove retired credentials explicitly
when no service uses them.

A host-side `flock` serializes deployment operations. Interrupted applies keep
`~/.local/state/homelab/config-pending.json`; retrying reconciles both old and
partially applied ownership. Never delete the journal to work around an error.

Destroy stops owned user units and removes owned files from `.config`, including
ownership recorded by interrupted applies. It preserves application data,
Podman volumes/networks, credentials, and the host firewall. Files unknown to the
manifest are preserved. One resource must own a user's configuration tree; do not
point multiple resources or states at the same home directory.

## SSH prerequisites

The target needs Linux, OpenSSH with SFTP and POSIX rename support, rootless
Podman/Quadlet, a working systemd user manager, `flock`, nftables, SELinux tools,
and passwordless sudo for host configuration. FCOS already supplies these.

The provider checks `~/.ssh/known_hosts` by default. Alternatively configure a
trusted public `host_key`. Unknown or changed keys are rejected; verify a newly
created VM's key through a trusted console before adding it to known_hosts.
OpenSSH client config, ProxyJump and password authentication are not evaluated.
Use `private_key_file` or an existing `SSH_AUTH_SOCK`; encrypted private keys must
be loaded into the agent. Remote home paths must be absolute without symlinks
(use `/var/home/core` on FCOS).

## Development

Use Go 1.27, Make and golangci-lint 2.13.2. Builds use `CGO_ENABLED=0`.

```sh
make build
make test vet lint
```

Tests use temporary directories, fake host operations and a loopback SSH/SFTP
server. They do not access a real homelab or external secret stores. Filesystem
tests check interrupted applies, drift repair, ownership and credential
bytes/modes; protocol tests check schema validation, defaults, unknown values,
write-only nullification and fingerprints independent of secret values.

`make testacc` runs Terraform CLI acceptance tests against a temporary local
configuration. Set `TF_ACC_TERRAFORM_PATH` to an absolute OpenTofu path to test
OpenTofu instead. Acceptance coverage includes the two functions and a deployment
plan; it does not run deployment CRUD against a live host. CI uses the template
Terraform 1.13/1.14 matrix and also checks OpenTofu 1.12.6.

`make generate` runs copywrite, formats examples and regenerates schema
documentation through `go generate` in the separate `tools` module. It updates
copyright headers as well as docs. The two tools use Go's `tool` directive;
generated content is checked for drift in the template's generate job.

For local use, build the provider and configure `dev_overrides` in the homelab's
ignored `.terraformrc`. See the
[homelab configuration instructions](../homelab/README.md#applying-configuration-changes).

## Releases

A `v*` tag runs one GoReleaser job on Ubuntu. Go cross-compiles Linux and macOS
amd64/arm64 binaries with CGO disabled. GoReleaser creates the registry ZIPs,
manifest, SHA-256 checksums, GPG signature and GitHub release, following the
scaffolding configuration. Release tooling uses GoReleaser 2.18.1.

Run `goreleaser check` to validate release configuration. Use
`goreleaser build --snapshot --single-target` to check a release binary
for the current platform locally without publishing anything.
