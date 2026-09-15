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

variable "messages" {
  type = map(string)
  default = {
    alpha = "Hello from alpha"
    beta  = "Hello from beta"
  }
  validation {
    condition     = alltrue([for name in keys(var.messages) : contains(["alpha", "beta"], name)])
    error_message = "This example supports the alpha and beta stacks. Omit an entry to destroy that stack."
  }
}

resource "quadlet_deployment" "network" {
  name = "example-network"
  files = {
    "containers/systemd/example.network" = file("${path.module}/shared.network")
  }
  restart = ["example-network.service"]
}

resource "quadlet_deployment" "app" {
  for_each   = var.messages
  depends_on = [quadlet_deployment.network]

  name = "example-${each.key}"
  files = {
    "containers/systemd/example-${each.key}.container"                   = templatefile("${path.module}/app.container.tftpl", { name = each.key })
    "containers/systemd/example-${each.key}.container.d/10-restart.conf" = file("${path.module}/restart.conf")
    "systemd/user/example-${each.key}.target"                            = templatefile("${path.module}/app.target.tftpl", { name = each.key })
    "example-${each.key}/index.html"                                     = each.value
  }
  restart = ["example-${each.key}.target"]
  enable  = ["example-${each.key}.target"]
}

output "owned_units" {
  value = {
    network = quadlet_deployment.network.units
    apps    = { for name, app in quadlet_deployment.app : name => app.units }
  }
}
