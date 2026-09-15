variable "app_password" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "quadlet_podman_secret" "app" {
  name     = "app-db-password"
  value_wo = var.app_password
  version  = "1"
}
