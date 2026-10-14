#!/bin/sh
# Cloud Run deploy is --max-instances 1, --execution-environment gen2, --no-cpu-throttling.
# Do not FUSE-mount a GCS bucket as the live database.
set -eu

if [ -z "${SQLITE_PATH:-}" ]; then
  echo "SQLITE_PATH is required" >&2
  exit 1
fi

mkdir -p "$(dirname "$SQLITE_PATH")"

if [ -n "${LITESTREAM_REPLICA_URL:-}" ]; then
  if [ -f "$SQLITE_PATH" ]; then
    echo "SQLite file already present; skipping restore"
  else
    echo "No local SQLite file; restoring from replica if it exists"
    litestream restore -if-replica-exists -o "$SQLITE_PATH" "$LITESTREAM_REPLICA_URL"
  fi
  exec litestream replicate -exec "/raices" "$SQLITE_PATH" "$LITESTREAM_REPLICA_URL"
fi

exec /raices
