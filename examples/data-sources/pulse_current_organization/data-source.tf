data "pulse_current_organization" "this" {}

output "pulse_organization_id" {
  value = data.pulse_current_organization.this.id
}
