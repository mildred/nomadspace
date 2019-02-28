job "ns-hello" {

  datacenters = ["dc-1"]

  group "ns" {
    task "na" {
      driver = "docker"
      config {
        image = "quay.io/mildred/nomadspace"
        network_mode = "host"
      }
      resources {
        cpu = 100
        memory = 64
      }
      artifact {
        source      = "git::https://github.com/mildred/nomadspace.git"
        destination = "local/nomadspace"
      }
      env {
        "NOMAD_ADDR"           = "http://127.0.0.1:4646"
        "NOMADSPACE_INPUT_DIR" = "${NOMAD_TASK_DIR}/nomadspace/samples/hello"
      }
    }
  }
}
