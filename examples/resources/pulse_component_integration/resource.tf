resource "pulse_component_integration" "example_alert" {
  component_id     = pulse_component.example_alert.id
  source           = "grafana"
  source_key       = "production/platform/example-service/grafana-rule-001"
  rotation_trigger = "2026-08-initial"
}
