# Terraform Provider for Pulse

The Pulse provider manages organization-scoped Pulse configuration through Terraform. It is built with the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) and uses protocol version 6.

> [!IMPORTANT]
> This repository currently contains the provider and authenticated client foundation only. It intentionally exposes no resources or data sources until the Pulse automation API contract is finalized.

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

The provider block can set `api_url` and `token` explicitly, but environment variables are recommended so credentials do not enter configuration files. `PULSE_API_TOKEN` is an organization-scoped automation credential; it must not be Pulse's platform-wide internal API token or a webhook-ingestion token.

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

Tags in the form `vX.Y.Z` trigger a signed GoReleaser build. Release assets follow the Terraform Registry naming and checksum conventions and include protocol metadata from `terraform-registry-manifest.json`.

Publishing requires repository administrators to configure an RSA or DSA GPG signing key, GitHub Actions secrets, and the `sreforce` namespace in the public Terraform Registry. See [RELEASING.md](RELEASING.md) for the manual prerequisites.

## License

Mozilla Public License 2.0. See [LICENSE](LICENSE).

