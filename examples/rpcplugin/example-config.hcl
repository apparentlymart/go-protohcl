
plugin {
  name    = "example"
  project = "awesomeapp"

  service "frontend" {
    argv = ["awesome-frontend"]
  }

  service "api" {
    argv = ["awesome-api"]
  }
}

result = {
  app_id = plugin.id
  services = [
    for sid in plugin.service_ids : {
      app_id     = plugin.id
      service_id = sid
    }
  ]
}
