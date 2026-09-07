## 0.1.0 (Unreleased)

FEATURES:

- Add `homelab-helpers_deployment`: verified SSH/SFTP, Quadlet and nftables
  validation, write-only secret installation, drift detection and interruption
  recovery. Adopt existing homelab manifests and pending journals.

ENHANCEMENTS:

- Update to Go 1.27 and align project tooling with HashiCorp scaffolding ae0e7c8.
- Build Linux and macOS amd64/arm64 releases in one GoReleaser job without CGO.
- Use isolated function-test fixtures and add deployment protocol/engine tests.
- Check Terraform 1.13/1.14 and OpenTofu in the template's test workflow.
