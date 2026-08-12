data "pulse_component" "example_service" {
  id = "00000000-0000-0000-0000-000000000001"
}

output "example_service_external_key" {
  value = data.pulse_component.example_service.external_key
}
