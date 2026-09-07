# Examples

These examples also supply code snippets for generated provider documentation:

- `provider/provider.tf` configures the provider.
- `functions/<name>/function.tf` demonstrates each provider function.
- `resources/homelab-helpers_deployment/resource.tf` shows the deployment resource.

Run `make generate` from the repository root after changing examples or schema
Markdown descriptions. It formats HCL and invokes the pinned `tfplugindocs`
tool through `tools/tools.go`, following the scaffolding layout.

The deployment example requires a configured FCOS host. It is documentation,
not a fixture to apply during local testing. There is no import script: a
resource ID alone cannot recover the connection settings and desired files.
