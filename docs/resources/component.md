---
page_title: "pulse_component Resource - pulse"
subcategory: "Components"
description: |-
  Manages one organization-scoped Pulse component.
---

# pulse_component (Resource)

Manages the desired configuration of one Pulse component. Runtime state is read
from Pulse but is computed-only, so Grafana signals do not create Terraform
configuration drift.

Display names may repeat. `external_key` is the stable, organization-unique
automation identity. Pulse uses it to recover a lost create response and to
restore the same component UUID after archival.

Deleting this resource archives the component instead of deleting operational
history. Import uses the Pulse component UUID.

## Example Usage

```terraform
resource "pulse_component" "sequencer" {
  external_key      = "main-net/core/citrea-sequencer"
  kind              = "rollup"
  name              = "Sequencer"
  component_type_id = data.pulse_component_type.service.id
  owner_team_id      = data.pulse_team.platform.id
  alert_enabled      = false
}
```

## Schema

### Required

- `component_type_id` (String) UUID of an existing component type in the authenticated organization.
- `external_key` (String) Organization-unique automation identity. Changing it replaces the Terraform resource.
- `kind` (String) Immutable component mode: `external` or `rollup`. Changing it requires replacement and a new `external_key`.
- `name` (String) Human-readable component name. Names are not unique.

### Optional

- `alert_enabled` (Boolean) Whether this component may initiate Pulse operational alerting. Defaults to `false`.
- `owner_team_id` (String) UUID of the owning team in the authenticated organization.
- `filter_tag_ids` (Set of String) Organization filter-tag UUIDs attached to the component. Defaults to an empty set.
- `relevance_tag_ids` (Set of String) Organization relevance-tag UUIDs attached to the component. Defaults to an empty set.

### Read-only

- `configuration_revision` (Number) Configuration-only optimistic-concurrency revision.
- `id` (String) Pulse component UUID.
- `state` (String) Current computed runtime state.

## Import

Import an existing component with its Pulse UUID:

```shell
terraform import pulse_component.sequencer 8afcf52d-7046-42b7-a1ac-876f70ed2c21
```

After import, run `terraform plan` and add the returned configuration attributes
to HCL. Runtime `state` is computed and should not be configured.
