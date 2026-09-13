## 0.3.0 (Unreleased)

BREAKING CHANGES:

- `homelab_config` derives `units` and `groups` from `files`; both are now
  computed and must be removed from configuration. The provider reproduces the
  Quadlet generator's unit naming, groups a pod with its containers, and
  fingerprints each group over its unit definitions, the shared networks,
  volumes and engine configuration, and the configuration it bind mounts from
  the user configuration directory. Fingerprints are byte-compatible with
  `sha256(jsonencode(...))`, so hosts keep their recorded groups and nothing
  restarts on the upgrade.

## 0.1.0 (Unreleased)

FEATURES:

- Add `homelab_config`: verified SSH/SFTP, Quadlet and nftables
  validation, write-only secret installation, drift detection and interruption
  recovery.

ENHANCEMENTS:

- Add `data_root` to `homelab_config`: create the bind mount sources a container
  needs before its unit starts, read from the volume arguments of the units
  Quadlet generates. Only sources below the root are created, the root itself
  never is, existing directories are left untouched and none is ever removed.
- Use the local provider name `homelab`.
- Update to Go 1.27 and align project tooling with HashiCorp scaffolding ae0e7c8.
- Build Windows, Linux and macOS amd64/arm64 releases in one GoReleaser job without CGO.
- Use isolated function-test fixtures and add deployment protocol/engine tests.
- Check Terraform 1.13/1.14 and OpenTofu in the template's test workflow.
