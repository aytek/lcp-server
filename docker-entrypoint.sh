#!/bin/sh
set -e

# Writes the LCP provider private key from an env var (base64-encoded PEM) to disk
# before starting the server. The key lives only in process memory / container FS,
# never in the committed repo.
#
# Required env: LCPSERVER_PRIVATE_KEY_B64  (base64 of PEM-encoded EC private key)

KEY_PATH="${LCPSERVER_CERTIFICATE_PRIVATEKEY:-/config/itech-drm/server-key.pem}"

if [ -n "$LCPSERVER_PRIVATE_KEY_B64" ]; then
    mkdir -p "$(dirname "$KEY_PATH")"
    printf '%s' "$LCPSERVER_PRIVATE_KEY_B64" | base64 -d > "$KEY_PATH"
    chmod 600 "$KEY_PATH"
elif [ ! -f "$KEY_PATH" ]; then
    echo "FATAL: LCP private key missing. Set LCPSERVER_PRIVATE_KEY_B64 or mount $KEY_PATH" >&2
    exit 1
fi

exec /app/lcpserver "$@"
