resource "quadlet_deployment" "app" {
  name = "app"
  files = {
    "containers/systemd/app.container" = <<-UNIT
      [Container]
      Image=docker.io/library/nginx:stable
      Volume=%E/app:/etc/nginx/conf.d:ro
      [Install]
      WantedBy=default.target
    UNIT
    "app/default.conf"                 = "server { listen 80; }\n"
  }

  restart = ["app.service"]
}

resource "quadlet_deployment" "worker" {
  name = "worker"
  files = {
    "systemd/user/worker.service" = <<-UNIT
      [Service]
      ExecStart=/bin/sh %E/worker/run.sh
      [Install]
      WantedBy=default.target
    UNIT
    "worker/run.sh"               = "exec sleep infinity\n"
  }

  restart = ["worker.service"]
  enable  = ["worker.service"]
  triggers = {
    configuration_revision = "1"
  }
}
