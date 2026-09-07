# Reconcile a service when any nested configuration file changes.
output "service_config_hash" {
  value = provider::homelab-helpers::dirhash("${path.module}/configs", "**")
}
