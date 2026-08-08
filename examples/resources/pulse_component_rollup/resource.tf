resource "pulse_component_rollup" "sequencer" {
  parent_component_id = pulse_component.sequencer.id

  # Rules are evaluated in this order. Children inside a rule are a set.
  rules = [
    {
      child_component_ids = [
        pulse_component.sequencer_panic.id,
        pulse_component.sequencer_commitment.id,
      ]
      when_child_yellow = "yellow"
      when_child_red    = "red"
    },
  ]
}
