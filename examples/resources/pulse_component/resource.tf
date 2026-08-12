resource "pulse_component" "sequencer" {
  external_key      = "main-net/core/citrea-sequencer"
  kind              = "rollup"
  name              = "Sequencer"
  component_type_id = data.pulse_component_type.service.id
  owner_team_id     = data.pulse_team.platform.id
  alert_enabled     = false
}
