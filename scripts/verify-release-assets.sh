#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:-dist}"
signature_mode="${2:-}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
  echo "release asset verification failed: $*" >&2
  exit 1
}

[[ -d "$dist_dir" ]] || fail "distribution directory $dist_dir does not exist"

shopt -s nullglob
checksum_files=("$dist_dir"/terraform-provider-pulse_*_SHA256SUMS)
[[ ${#checksum_files[@]} -eq 1 ]] || fail "expected exactly one SHA256SUMS file"

checksum_file="${checksum_files[0]}"
release_prefix="$(basename "$checksum_file" _SHA256SUMS)"
release_version="${release_prefix#terraform-provider-pulse_}"
[[ "$release_version" != "$release_prefix" ]] || fail "could not derive the provider version"

manifest_file="$dist_dir/${release_prefix}_manifest.json"
if [[ ! -f "$manifest_file" ]]; then
  cp "$repository_root/terraform-registry-manifest.json" "$manifest_file"
fi

jq -e '.version == 1 and .metadata.protocol_versions == ["6.0"]' "$manifest_file" >/dev/null \
  || fail "Registry manifest must declare protocol 6.0"

platforms=(darwin_amd64 darwin_arm64 linux_amd64 linux_arm64)
for platform in "${platforms[@]}"; do
  archive="$dist_dir/${release_prefix}_${platform}.zip"
  [[ -f "$archive" ]] || fail "missing archive for $platform"

  archive_entries="$(unzip -Z1 "$archive")"
  if printf '%s\n' "$archive_entries" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
    fail "$archive contains an unsafe path"
  fi
  [[ "$(printf '%s\n' "$archive_entries" | grep -Fxc "terraform-provider-pulse_v${release_version}")" == "1" ]] \
    || fail "$archive must contain exactly one correctly versioned provider binary"
done

archives=("$dist_dir"/"$release_prefix"_*.zip)
[[ ${#archives[@]} -eq ${#platforms[@]} ]] \
  || fail "release must contain exactly the four supported platform archives"

checksum_entries="$(awk '{ print $2 }' "$checksum_file")"
[[ "$(printf '%s\n' "$checksum_entries" | sed '/^$/d' | wc -l | tr -d ' ')" == "5" ]] \
  || fail "checksum file must contain four archives and one Registry manifest"

for platform in "${platforms[@]}"; do
  expected_name="${release_prefix}_${platform}.zip"
  printf '%s\n' "$checksum_entries" | grep -Fx "$expected_name" >/dev/null \
    || fail "checksum file does not include $expected_name"
done
printf '%s\n' "$checksum_entries" | grep -Fx "${release_prefix}_manifest.json" >/dev/null \
  || fail "checksum file does not include the Registry manifest"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist_dir" && sha256sum --check "$(basename "$checksum_file")")
elif command -v shasum >/dev/null 2>&1; then
  (cd "$dist_dir" && shasum -a 256 -c "$(basename "$checksum_file")")
else
  fail "sha256sum or shasum is required"
fi

if [[ "$signature_mode" != "--allow-unsigned" ]]; then
  signature_file="${checksum_file}.sig"
  [[ -f "$signature_file" ]] || fail "missing detached checksum signature"
  signature_status="$(gpg --batch --status-fd=1 --verify "$signature_file" "$checksum_file")" \
    || fail "detached checksum signature is invalid"
  validsig_record="$(printf '%s\n' "$signature_status" | awk '$2 == "VALIDSIG" { print; exit }')"
  signing_fingerprint="$(printf '%s\n' "$validsig_record" | awk '{ print $3 }')"
  reported_primary_fingerprint="$(printf '%s\n' "$validsig_record" | awk '{ print $NF }')"
  [[ -n "$signing_fingerprint" ]] || fail "signature did not report a valid signing fingerprint"

  if ! printf '%s\n' "$reported_primary_fingerprint" | grep -Eq '^[0-9A-Fa-f]{40}([0-9A-Fa-f]{24})?$'; then
    reported_primary_fingerprint=""
  fi

  if [[ -n "${EXPECTED_GPG_FINGERPRINT:-}" ]]; then
    expected_fingerprint="$(printf '%s' "$EXPECTED_GPG_FINGERPRINT" | tr '[:lower:]' '[:upper:]')"
    signing_fingerprint="$(printf '%s' "$signing_fingerprint" | tr '[:lower:]' '[:upper:]')"
    reported_primary_fingerprint="$(printf '%s' "$reported_primary_fingerprint" | tr '[:lower:]' '[:upper:]')"
    if [[ "$expected_fingerprint" != "$signing_fingerprint" && "$expected_fingerprint" != "$reported_primary_fingerprint" ]]; then
      fail "checksum signature does not match the imported release key"
    fi
  fi
fi

echo "Verified Terraform Registry release assets for ${release_version}."
