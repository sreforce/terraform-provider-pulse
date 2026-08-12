resource "pulse_component_integration" "example_service_grafana" {
  component_id         = pulse_component.example_service.id
  integration_provider = "grafana"
  rotation_trigger     = "2026-08-initial"
}
