resource "pulse_component_rollup" "example_service" {
  parent_component_id = pulse_component.example_service.id

  # Rules are evaluated in this order. Children inside a rule are a set.
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
