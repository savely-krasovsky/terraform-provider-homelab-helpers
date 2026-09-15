# Terraform Provider for Quadlet

Deploy Quadlets, native systemd user units and configuration files over SSH or
locally on Linux. Requires Terraform or OpenTofu 1.11+.

- [`quadlet_deployment`](docs/resources/deployment.md) installs files, discovers
  their units and activates the deployment when files, triggers or policy change.
- [`quadlet_podman_secret`](docs/resources/podman_secret.md) manages write-only
  Podman secrets with rotation versions and installation revisions.

## Usage

The target needs rootless Podman with Quadlet and a running systemd user manager.
This example runs as the local Linux user:

```hcl
terraform {
  required_providers {
    quadlet = {
      source = "savely-krasovsky/homelab-helpers"
    }
  }
}

provider "quadlet" {
  transport = "local"
}

resource "quadlet_deployment" "app" {
  name = "app"
  files = {
    "containers/systemd/app.container" = <<-UNIT
      [Container]
      Image=docker.io/library/nginx:stable
      [Install]
      WantedBy=default.target
    UNIT
  }
  restart = ["app.service"]
}
```

For SSH access, see [provider configuration](docs/index.md). The target needs
SFTP with POSIX rename, `flock`, `install` and `sync`; secret resources need the
Podman API socket.

## Deployment behavior

Use separate deployments for applications that should restart independently.
`restart` starts or restarts selected units; `try_restart` only restarts active
ones. Quadlets use `[Install]` for boot activation; `enable` manages native units.
See the [independent applications example](examples/independent-stacks) for shared networking.

Managed files are stored in Terraform state. Keep secret values in
`quadlet_podman_secret`, bump `version` to rotate them, and reference their
`revision` in deployment `triggers` to activate consumers.

Overlapping file or unit ownership is rejected. Failed deployments are retried
on the next apply. Destroy stops owned units and removes their configuration;
application data stays. Initial setup requires a fresh host deployment and
fresh Terraform state. Import recovers resources recorded by this provider.

## Development

Requires Go 1.27, Make and golangci-lint 2.13.2. Install Podman for Quadlet tests
and Terraform for acceptance tests and documentation generation.

```sh
make build
make test vet lint
make testacc
make generate
```

For OpenTofu acceptance tests, set `TF_ACC_TERRAFORM_PATH` to the binary path.
The [live smoke test](tests/README.md) uses a disposable SSH VM and includes a reboot.
A `v*` tag publishes Windows, Linux and macOS builds for amd64 and arm64.
