# Discover immediate subdirectories containing service configuration.
output "service_directories" {
  value = provider::homelab-helpers::dirset("${path.module}/configs", "*")
}
