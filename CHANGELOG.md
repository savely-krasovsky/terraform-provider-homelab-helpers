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
