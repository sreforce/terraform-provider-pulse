# Terraform Provider for Pulse

The Pulse provider manages organization-scoped Pulse configuration through Terraform. It is built with the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) and uses protocol version 6.

> [!IMPORTANT]
> The `0.1.x` line targets Pulse's organization-scoped `/api/automation/v1` contract. Do not point it at Pulse's platform-wide `/api/v1` API or give it a platform internal token.

The initial provider surface manages components, complete rollup definitions, and component-bound Grafana integrations. It also provides read-only lookups for the current organization, components, component types, teams, and tags. Runtime component state is observed but never submitted as desired configuration.

## Configuration

Keep credentials out of Terraform configuration and set both provider environment variables in the process that runs Terraform:

```shell
export PULSE_API_URL="https://pulse.example.com"
export PULSE_API_TOKEN="<organization-scoped-automation-token>"
```

```terraform
terraform {
  required_providers {
    pulse = {
      source = "sreforce/pulse"
    }
  }
}

provider "pulse" {}
```

The provider block can set `api_url` and `token` explicitly, but environment variables are recommended so credentials do not enter configuration files. `PULSE_API_TOKEN` is an organization-scoped automation credential; it must not be Pulse's platform-wide internal API token or a webhook-ingestion token. Pulse API and component-integration endpoints require HTTPS. Plain HTTP can be enabled only for an explicit loopback development endpoint with `allow_insecure_http = true`.

Component-integration credentials are returned once and retained as sensitive Terraform state so another provider can configure the corresponding Grafana contact point. Terraform's sensitive flag masks display; it does not remove that secret from current or historical state. Restrict state access and rotate or revoke a component integration before treating an exposed state version as safe.

## Development

Requirements:

- Go version compatible with `go.mod`
- Terraform CLI for formatting examples and generating documentation
- `golangci-lint` for `make lint`

Common commands:

```shell
make check
make lint
make generate
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and API compatibility rules.

## Releasing and publishing

Tags in the form `vX.Y.Z` trigger a signed GoReleaser build. Release assets follow the Terraform Registry naming and checksum conventions, include protocol metadata from `terraform-registry-manifest.json`, and receive GitHub build-provenance attestations before the draft release is published. The first release line includes Linux and macOS builds for both AMD64 and ARM64; Linux ARM64 is the Atlantis runtime target.

Publishing requires repository administrators to configure an RSA or DSA GPG signing key, GitHub Actions secrets, repository security settings, and the `sreforce` namespace in the public Terraform Registry. See [RELEASING.md](RELEASING.md) for the manual prerequisites and verification procedure.

## License

Mozilla Public License 2.0. See [LICENSE](LICENSE).
