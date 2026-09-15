# Examples

Snippets for the generated documentation: `provider/provider.tf` configures the
provider and `resources/<type>/resource.tf` shows each resource. They are
documentation, not fixtures to apply. Run `make generate` after changing them.

[`independent-stacks`](independent-stacks) is a runnable local example: two
applications with native targets and separate activation share one network.
Its README explains creation, updating one stack and deleting it independently.

The provider example includes SSH and a local alias. Local mode requires Linux;
resources using that alias set `provider = quadlet.local`. Socket overrides are
paths on the selected target, not additional transports.
