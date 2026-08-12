# Changelog

## 0.3.0 (2026-08-12)

FEATURES:

- Manage organization-scoped component types, teams, and tags with import, optimistic concurrency, and protected deletion.
- Expose configuration revisions and complete mutable catalog fields through the matching data sources.

SCOPE:

- Keep organizations, users, memberships, BFF APIs, governance settings, and operational alert state outside Terraform.

## 0.2.2 (2026-08-12)

FIXES:

- Defer catalog selector validation until Terraform resolves referenced variables during planning.

## 0.2.1 (2026-08-12)

FIXES:

- Resolve catalog data sources by a configured selector when Terraform represents the unconfigured Optional+Computed selector as unknown.

## 0.2.0 (2026-08-12)

BREAKING CHANGES:

- Remove the `kind` attribute from component resources and data sources. A component may now receive direct signals and own children or rollup rules concurrently.
- Replace Grafana-only integration `source` and `source_key` identity with `integration_provider`. Supported values are `grafana`, `pagerduty`, and `pulse`.
- Use the natural `{component_uuid}/{provider}` integration import identity and remove the public integration `id`, `status`, and `observed_version` attributes. The active credential identifier is exposed as computed `version`.

FEATURES:

- Manage provider-specific component ingestion endpoints at `/webhooks/components/{component_id}/{provider}`.
- Preserve explicit rotation, one-time-secret drift recovery, and atomic adoption of human-owned integrations.
- Treat ingestion-created dynamic children as server-managed topology outside the static rollup rules owned by Terraform.

UPGRADE NOTES:

- Existing component state drops the former `kind` value during schema upgrade.
- Existing Grafana integration state migrates to `integration_provider = "grafana"` while preserving a still-valid one-time secret and credential version.
- Update configuration to remove `kind` and replace `source` plus `source_key` with `integration_provider` before applying with v0.2.

## 0.1.1 (2026-08-12)

DOCUMENTATION:

- Replace deployment-specific examples and test fixtures with neutral examples.

## 0.1.0 (2026-08-12)

FEATURES:

- Initial Terraform Plugin Framework provider scaffold.
- Environment-backed Pulse API configuration.
- Authenticated, mockable HTTP client foundation.
- Typed organization-scoped `/api/automation/v1` client with safe retries, revisions, and stable errors.
- Component, aggregate rollup, and component-bound Grafana integration resources.
- Current-organization, component, component-type, team, and tag data sources.
- Reproducible Linux and macOS release packages for AMD64 and ARM64.
- Signed checksum, Registry manifest, and build-provenance verification.
- CodeQL, dependency review, and Go vulnerability checks.

SECURITY:

- Upgrade the Go release toolchain and transitive networking dependencies to versions without known reachable vulnerabilities.
