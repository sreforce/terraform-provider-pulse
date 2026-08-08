---
page_title: "pulse_component_integration Resource - pulse"
subcategory: ""
description: |-
  Manages the component-bound Grafana ingestion integration for one external Pulse component.
---

# pulse_component_integration (Resource)

Manages the single Grafana ingestion integration bound to an external Pulse component.

The provider authenticates with the organization's automation key, but Grafana receives the much narrower component-bound `secret`. That secret can send signals only through this integration. Rollup components cannot have integrations.

`source_key` is the immutable identity of one reviewed Grafana mapping. To use another key, create another external alert-leaf component and bind the new integration there; do not rename a key on an existing leaf.

Pulse reveals `secret` only when the integration is created, rotated, or adopted. Terraform necessarily retains that value in its encrypted, versioned state so it can configure a Grafana contact point. Restrict state access and rotate the credential if state exposure is suspected.

## Example Usage

```terraform
resource "pulse_component_integration" "sequencer_commitment" {
  component_id     = pulse_component.sequencer_commitment.id
  source           = "grafana"
  source_key       = "sequencer-commitment"
  rotation_trigger = "2026-08-initial"
}

resource "grafana_contact_point" "sequencer_commitment_pulse" {
  name = "Pulse - Sequencer commitment"

  webhook {
    url                       = pulse_component_integration.sequencer_commitment.endpoint
    authorization_scheme      = "Bearer"
    authorization_credentials = pulse_component_integration.sequencer_commitment.secret
  }
}
```

## Rotation

Change `rotation_trigger` to any new, non-secret value. The provider asks Pulse to issue a successor credential and atomically retires the unreachable/current predecessor according to Pulse's bounded overlap policy.

Do not place secret material in `rotation_trigger`; it is intentionally non-sensitive and appears in plans.

## Import and adoption

Import uses the bound component UUID, not the integration UUID:

```shell
terraform import pulse_component_integration.sequencer_commitment 11111111-1111-4111-8111-111111111111
```

Pulse never returns existing plaintext credentials, so import cannot populate `secret`. Configure a new `rotation_trigger` and apply to rotate immediately.

If the imported integration is human-owned, also set `adopt = true`. Adoption is explicit because it transfers lifecycle ownership to automation and rotates the credential atomically. The UI cannot rotate an automation-owned integration afterward.

## Drift behavior

If Pulse reports a credential version different from the version associated with Terraform's stored secret, the provider clears the unusable secret, sets `rotation_required`, and warns. Planning is blocked while `rotation_trigger` is unchanged. Change it to a new value and apply; the provider never pretends it can recover the old plaintext.

Deleting the Terraform resource archives and disables the integration. It does not hard-delete its history.

## Schema

### Required

- `component_id` (String) UUID of the external Pulse component receiving this integration.
- `rotation_trigger` (String) Non-secret caller-controlled value. Changing it rotates the ingestion credential.
- `source` (String) Integration source. Version 0.1 supports only `grafana`.
- `source_key` (String) Immutable mapping identity that Grafana sends as `pulse_alert_key`.

### Optional

- `adopt` (Boolean) Explicitly permit adoption of a human-owned integration. Defaults to `false`.

### Read-Only

- `endpoint` (String) Component-bound Grafana webhook endpoint.
- `id` (String) Pulse component-integration UUID.
- `lifecycle_owner` (String) `human` or `automation`.
- `observed_version` (String) Credential-version UUID associated with `secret`.
- `revision` (Number) Configuration revision used for optimistic concurrency.
- `rotation_required` (Boolean) Whether an import or out-of-band credential change requires deliberate rotation.
- `secret` (String, Sensitive) One-time Grafana ingestion secret retained in Terraform state.
- `status` (String) Integration lifecycle status.
