resource "pulse_component" "example_service" {
  external_key      = "production/platform/example-service"
  kind              = "rollup"
  name              = "Example service"
  component_type_id = data.pulse_component_type.service.id
  owner_team_id     = data.pulse_team.platform.id
  alert_enabled     = false
}
