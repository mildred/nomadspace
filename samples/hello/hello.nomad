job "hello" {

  datacenters = ["dc-1"]

  group "hello" {
    task "hello" {
      driver = "docker"
      config {
        image = "mildred/hello"
        port_map {
          http = 80
        }
      }
      resources {
        cpu = 100
        memory = 64
        network {
          port "http" {}
        }
      }
      service {
        name = "hello"
        port = "http"
      }
    }
  }
}
