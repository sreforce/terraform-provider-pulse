data "pulse_tag" "network" {
  purpose = "filter"
  name    = "network"
}

output "network_tag_id" {
  value = data.pulse_tag.network.id
}
