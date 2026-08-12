# Release signing key

Pulse provider release checksums are signed by the dedicated RSA-4096 key:

```text
SRE Force Pulse Terraform Provider <emre@sreforce.com>
42B2 B520 FCD0 E7DC CD3F B62A B848 78AE 9A22 D133
```

Import `terraform-provider-pulse-signing-key.asc` to verify a release locally.
The private recovery kit is intentionally absent from Git and is stored only
under the ignored `.release-private/` directory in the maintainer checkout.
