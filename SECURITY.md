# Security Policy

## Supported versions

Security fixes target the latest `0.1.x` release unless a wider backport is announced.

## Reporting a vulnerability

Report vulnerabilities privately through this repository's GitHub Security Advisories feature. Do not open a public issue for suspected credential exposure, authorization bypass, cross-organization access, or another exploitable weakness.

Include the affected provider version, Terraform version, reproduction steps, impact, and any suggested mitigation. Never include live Pulse tokens, one-time integration credentials, plans, crash logs, or Terraform state in a report. Use synthetic credentials and redact Pulse URLs when they identify a private deployment.

Private vulnerability reporting is the supported disclosure channel. Repository administrators must keep it enabled and limit advisory access to the maintainers coordinating the fix.

## Security expectations

- Provider and dependency changes must pass CodeQL, dependency review, and `govulncheck`.
- Release workflow dependencies are pinned by commit and updated through reviewed pull requests.
- Public releases are checksummed, GPG-signed, and accompanied by GitHub build-provenance attestations.
- Provider diagnostics and logs must never include credentials or raw API error bodies.
- Acceptance tests must use disposable Pulse organizations and credentials; they must never target production by default.

The `Sensitive` Terraform schema flag masks display but does not remove a value from state. Resource documentation must identify every secret retained in Terraform state and its required rotation and revocation procedure before that resource is released.
