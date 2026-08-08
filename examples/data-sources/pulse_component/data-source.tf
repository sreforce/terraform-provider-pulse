data "pulse_component" "sequencer" {
  id = "00000000-0000-0000-0000-000000000001"
}

output "sequencer_external_key" {
  value = data.pulse_component.sequencer.external_key
}
