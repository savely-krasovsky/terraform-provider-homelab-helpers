# Independent stacks

This runnable example creates three deployments for the local Linux user:
one shared Podman network and two HTTP applications, `alpha` and `beta`.
It requires a running systemd user manager and rootless Podman with Quadlet.
Use an otherwise unused user account for this example. The applications do not
publish host ports.

```sh
tofu init
tofu apply
tofu plan
```

Terraform 1.11+ can also run these commands. The second plan has no changes.
Each application has a native target, a Quadlet container, a scoped drop-in and
its own configuration directory. The computed `owned_units` output includes
the targets, generated services and shared network service.

The network deployment starts `example-network.service`. Applications connect
to the actual Podman name `example`, rather than referencing a Quadlet
source in another deployment. Their `Requires=` and `After=` dependencies also
create the network before application startup after a host reboot. Terraform's
`depends_on` orders installation and destruction of the deployments.

The native target starts its application. The application's `PartOf=` propagates
target restarts and stops to its service. Shared restart settings are copied
into each application's own drop-in; no generic cross-deployment drop-in is used.

## Check independent activation

Record both application service invocations:

```sh
systemctl --user show -p InvocationID example-alpha.service example-beta.service
tofu apply -var='messages={alpha="Updated alpha",beta="Hello from beta"}'
systemctl --user show -p InvocationID example-alpha.service example-beta.service
tofu plan -var='messages={alpha="Updated alpha",beta="Hello from beta"}'
```

Only alpha's invocation changes. Its target is restarted because a managed file
changed. Beta has no change, and the last plan has no changes.

Omit alpha to destroy its deployment:

```sh
tofu apply -var='messages={beta="Hello from beta"}'
systemctl --user is-active example-beta.service example-network.service
tofu destroy
```

Beta and the shared network service stay active when alpha is removed. The final
destroy removes all example units and configuration. Podman images and the
network itself remain, as described by the provider's deployment contract.

The Go tests render these same templates, run the installed Quadlet generator
and exercise installation, independent activation, recovery and deletion in
temporary directories. They record systemd operations without starting containers
or changing the machine's user manager.
