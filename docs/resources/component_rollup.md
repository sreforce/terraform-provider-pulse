---
page_title: "pulse_component_rollup Resource - pulse"
subcategory: "Components"
description: |-
  Owns the complete ordered rollup ruleset for one Pulse component.
---

# pulse_component_rollup (Resource)

`pulse_component_rollup` owns the complete ordered rollup ruleset for one
parent component. Do not split one parent's rules across multiple Terraform
resources.

Rule order is significant. `child_component_ids` is an unordered set, and the
provider canonicalizes those UUIDs before writing and reading state. A child
may appear in only one rule.

An empty `rules = []` value is valid. Pulse keeps that rollup in `unknown`
rather than presenting it as healthy. A configured empty ruleset remains a
present, revisioned resource; it is distinct from a ruleset that has never been
configured or was deleted.

Pulse revisions protect the ruleset from lost updates. If someone changes the
same rollup outside Terraform after state was refreshed, apply stops with a
stale-revision diagnostic. Refresh, review the change, and plan again.

## Example Usage

```terraform
resource "pulse_component_rollup" "sequencer" {
  parent_component_id = pulse_component.sequencer.id

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
```

## Schema

### Required

- `parent_component_id` (String) Lowercase UUID of the rollup parent. Changing
  it replaces the resource.
- `rules` (List of Object) Complete ordered ruleset. The list may be empty.
  Each rule contains:
  - `child_component_ids` (Set of String) One or more lowercase component UUIDs.
  - `when_child_yellow` (String) `none`, `yellow`, or `red`.
  - `when_child_red` (String) `none`, `yellow`, or `red`.

### Read-only

- `revision` (Number) Pulse configuration revision used for optimistic
  concurrency.

## Import

Import with the rollup parent component UUID:

```shell
terraform import pulse_component_rollup.sequencer 2d6ef519-3bf8-4c79-9d7a-9e9d25b96d5f
```

Import never searches by display name because Pulse component names are not
unique.
