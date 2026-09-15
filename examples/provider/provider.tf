terraform {
  required_providers {
    quadlet = {
      source = "savely-krasovsky/homelab-helpers"
    }
  }
}

provider "quadlet" {
  host             = "192.0.2.10"
  user             = "deploy"
  private_key_file = "~/.ssh/id_ed25519"
  host_key         = "ssh-ed25519 AAAA..."
}

# Alternative: manage the Linux machine running Terraform as its process user.
# Omit host, user and every other SSH argument when using local mode.
provider "quadlet" {
  alias     = "local"
  transport = "local"
}
