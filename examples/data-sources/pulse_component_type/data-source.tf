data "pulse_component_type" "service" {
  name = "Service"
}

output "service_component_type_id" {
  value = data.pulse_component_type.service.id
}
