# Releasing

Provider releases are immutable, signed, semantic-versioned GitHub releases consumed by the public Terraform Registry.

## One-time setup

1. Keep the public GitHub repository named `terraform-provider-pulse` under the `sreforce` organization.
2. Generate a dedicated RSA or DSA GPG signing key. The Terraform Registry does not accept the default ECC key type.
3. Add the ASCII-armored public key to the `sreforce` namespace in Terraform Registry user settings.
4. Add the ASCII-armored private key as the GitHub Actions secret `GPG_PRIVATE_KEY` and its passphrase as `PASSPHRASE`.
5. Confirm organization and repository policies allow the pinned GitHub Actions used by `.github/workflows/release.yml`.
6. Sign in to the public Terraform Registry with a GitHub account that has administration access to the `sreforce` organization, then publish `sreforce/pulse` from this repository after the first signed release exists.

## Release procedure

1. Update `CHANGELOG.md` and run `make check`, `make lint`, and `make generate`.
2. Merge the reviewed release commit to `main`.
3. Create and push a new immutable semantic-version tag such as `v0.1.0`. Do not create a branch with the same name.
4. Verify the GitHub release contains platform ZIP files, the renamed registry manifest, SHA-256 checksums, and the detached checksum signature.
5. For the first release, finish publication in the Terraform Registry UI. Later GitHub releases are ingested through the Registry webhook.

Never replace assets on an existing release. Publish a new version if an artifact or provider behavior must change.

