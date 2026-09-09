# Discover immediate subdirectories containing service configuration.
output "service_directories" {
  value = provider::homelab::dirset("${path.module}/configs", "*")
}
