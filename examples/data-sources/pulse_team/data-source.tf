data "pulse_team" "operations" {
  name = "Operations"
}

output "operations_team_id" {
  value = data.pulse_team.operations.id
}
