# Releasing

Provider releases are immutable, signed, semantic-versioned GitHub releases consumed by the public Terraform Registry.

## One-time setup

1. Keep the public GitHub repository named `terraform-provider-pulse` under the `sreforce` organization.
2. Generate a dedicated RSA or DSA GPG signing key. The Terraform Registry does not accept the default ECC key type.
3. Add the ASCII-armored public key to the `sreforce` namespace in Terraform Registry user settings.
4. Keep the private recovery kit outside Git. If GitHub Actions are enabled,
   add the ASCII-armored private key as `GPG_PRIVATE_KEY` and its passphrase as
   `PASSPHRASE`; otherwise follow the local draft-first procedure below.
5. Enable private vulnerability reporting, Dependabot security updates, secret scanning with push protection, and code scanning for the repository. The checked-in security workflow provides CodeQL, dependency review, and `govulncheck`; repository settings remain an administrator responsibility.
6. Protect `main` with the required test, generated-file, release-package, and security checks. Restrict tag creation for `v*` releases to maintainers.
7. Confirm organization and repository policies allow the commit-pinned GitHub Actions used by `.github/workflows/release.yml`.
8. Confirm GitHub artifact attestations are available to the public repository.
9. Sign in to the public Terraform Registry with a GitHub account that has administration access to the `sreforce` organization, then publish `sreforce/pulse` from this repository after the first signed release exists.

The release workflow intentionally creates a draft GitHub release first. A
local release must follow the same draft-first boundary. Publish that draft
only after all of the following succeed:

- four expected ZIP archives exist: Linux AMD64/ARM64 and macOS AMD64/ARM64;
- every archive contains exactly one correctly versioned provider binary;
- the protocol 6.0 Registry manifest and all archives match the SHA-256 checksum file;
- the checksum file has a valid detached GPG signature from an RSA or DSA key;
- GitHub records build-provenance attestations when Actions are available.

If verification fails, the release remains a draft and must not be registered or consumed.

## Release procedure

1. Update `CHANGELOG.md`, replace `(Unreleased)` with the release date, and run `make check`, `make lint`, `make generate`, and `make release-check` with GoReleaser `v2.17.1`.
2. Merge the reviewed release commit to `main`.
3. Create a signed immutable semantic-version tag such as `v0.1.0`. Do not
   create a branch with the same name.
4. If Actions are unavailable, use GoReleaser `v2.17.1` with publishing and
   signing skipped, sign the generated checksum file with the dedicated key,
   verify `dist` with `scripts/verify-release-assets.sh`, push the tag, and
   upload the verified assets to a draft GitHub release.
5. Download the draft assets into a clean directory, verify them using only
   the committed public key, and only then publish the draft.
6. When Actions are available, verify the release attestation with GitHub CLI,
   for example `gh attestation verify <archive> --repo sreforce/terraform-provider-pulse`.
7. For the first release, finish publication in the Terraform Registry UI.
   Later GitHub releases are ingested through the Registry webhook.

Never replace assets on an existing release. Publish a new version if an artifact or provider behavior must change.
