#!/usr/bin/env bash
set -euo pipefail

# Global IME omits this intermediate from its TLS chain. Verify the CA's
# signature against system roots before adding it to a process-local bundle.
output=${1:?Usage: globalime-ca-bundle.sh OUTPUT_PATH}
roots=/etc/ssl/certs/ca-certificates.crt
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
curl --fail --silent --show-error --max-time 45 \
  http://crt.sectigo.com/SectigoPublicServerAuthenticationCADVR36.crt \
  -o "$scratch/issuer.der"
openssl x509 -inform DER -in "$scratch/issuer.der" -out "$scratch/issuer.pem"
openssl verify -CAfile "$roots" "$scratch/issuer.pem"
cat "$roots" "$scratch/issuer.pem" > "$output"
