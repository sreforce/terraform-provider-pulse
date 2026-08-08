# Contributing

Thank you for contributing to the Pulse Terraform provider.

## Development setup

You need a Go version compatible with `go.mod` and Terraform CLI for formatting examples and future acceptance tests.

```shell
make check
make generate
```

`make check` formats, builds, vets, and tests the provider. Run `make lint` when `golangci-lint` is installed locally.

## API contract changes

Do not add a resource, data source, or endpoint path until the corresponding public Pulse automation API contract is stable. Provider schemas are public interfaces: favor explicit attributes, import support, predictable lifecycle behavior, and backward-compatible changes.

Keep credentials out of source, test fixtures, logs, diagnostics, and examples. Use environment variables for local credentials.

## Documentation

Provider documentation under `docs/` is generated from provider schemas and examples. Update the schema or files under `examples/`, then run `make generate`.

## Pull requests

Keep changes focused, add tests, describe Pulse API compatibility, and include a safe rollback path. Acceptance tests that create remote objects must be opt-in and must clean up what they create.

