resource "pulse_component_integration" "sequencer_commitment" {
  component_id     = pulse_component.sequencer_commitment.id
  source           = "grafana"
  source_key       = "main-net/core/citrea-sequencer/grafana-ff1fyn5ah9vcwe"
  rotation_trigger = "2026-08-initial"
}
