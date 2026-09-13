resource "homelab_config" "fcos" {
  host             = "192.0.2.10"
  user             = "core"
  private_key_file = "~/.ssh/id_ed25519"
  known_hosts_file = "~/.ssh/known_hosts"

  files = {
    "containers/systemd/example.container" = <<-UNIT
      [Container]
      Image=docker.io/library/nginx:stable
      [Service]
      Restart=always
      RestartSec=10
      [Install]
      WantedBy=default.target
    UNIT
  }

  # units and groups are derived from files, and exported for inspection.

  # Volume sources below this root are created; the root itself must exist.
  data_root = "/var/mnt/docker/app_data"

  firewall = file("${path.module}/firewall.nft")
  secrets  = {} # name => non-secret source reference or version.

  # When secrets is nonempty, pass the matching name => value map here.
  # Prefer ephemeral resources so the source also keeps values out of state.
  secret_values_wo = {}
}
