resource "pulse_component_rollup" "example_service" {
  parent_component_id = pulse_component.example_service.id

  # Static rules are evaluated in this order. Ingestion-created dynamic
  # children remain server-managed and are not part of this resource.
  rules = [
    {
      child_component_ids = [
        pulse_component.example_failure.id,
        pulse_component.example_alert.id,
      ]
      when_child_yellow = "yellow"
      when_child_red    = "red"
    },
  ]
}
