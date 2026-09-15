## 0.4.0 (Unreleased)

- `quadlet_deployment` manages configuration files and discovered systemd units
  with deployment-wide activation, file drift detection, ownership checks,
  import and recovery after interrupted operations.
- `quadlet_podman_secret` manages write-only secrets with owner labels, rotation
  versions and installation revisions for consumer activation.
- Provider-level host configuration supports verified SSH and local Linux access.
- Quadlet validation uses the host generator, including generated unit aliases.
- Independent application examples and an opt-in live smoke test cover shared
  networking, secret recreation, failed activation recovery and reboot.

## 0.3.0

- `homelab_config` derives `units` and `groups` from `files`.

## 0.1.0

- Add `homelab_config`.
