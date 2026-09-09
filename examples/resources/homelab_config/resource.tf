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

  units = ["example.service"]
  groups = {
    example = {
      units        = ["example.service"]
      enable       = []
      hash         = "1" # Prefer sha256() over every file affecting the group.
      uses_secrets = false
    }
  }

  firewall = file("${path.module}/firewall.nft")
  secrets  = {} # name => non-secret source reference or version.

  # When secrets is nonempty, pass the matching name => value map here.
  # Prefer ephemeral resources so the source also keeps values out of state.
  secret_values_wo = {}
}
