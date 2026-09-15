# Live smoke test

`smoke.py` runs the independent stacks example through OpenTofu and the SSH
provider against **a disposable VM**, including a real reboot. It verifies
independent activation, secret recreation at an unchanged version, recovery
after a real activation failure, boot enablement, deletion and empty plans.
On failure it leaves the fixture and logs for inspection.

The controller needs Python 3, SSH, OpenTofu 1.11+ (or Terraform via `--cli`), and
a built provider. The VM needs rootless Podman with Quadlet, SFTP with POSIX
rename, a running systemd user manager and lingering enabled for the SSH user.
The system `network-online.target` must start at boot for Quadlet's network wait unit.
The user needs passwordless sudo for reboot. The test enables `podman.socket`
and reserves `example-*` deployments and `example-*` containers/secrets.
Use a fresh test user with no other workloads. No host ports are published.

```sh
make build
python3 tests/smoke.py \
  --host 127.0.0.1 --port 2222 --user tester \
  --identity /absolute/path/to/test-key \
  --known-hosts /absolute/path/to/test-known-hosts \
  --provider-dir "$PWD/bin" \
  --workdir /tmp/provider-smoke-results \
  --allow-reboot
```

`--workdir` must not exist. The script copies the example there, adds synthetic
write-only secrets and per-application revision triggers, and uses a temporary
CLI configuration pointing at the local provider build. It does not need
`tofu init` or a provider release. Output and `result.json` are retained there.

The failure case adds a temporary runtime drop-in with `ExecStartPre=/bin/false`
to alpha, changes its configuration, and expects apply to fail. It then removes
the fault and retries with the same desired files. Reboot is checked using a
changed kernel boot ID; systemd InvocationIDs establish which services restarted.

At the end, Terraform destroys the fixture. As in the provider's contract,
the pulled image and Podman network remain. Dispose of the VM after the test.
This test is opt-in and does not run as part of `go test ./...`.
