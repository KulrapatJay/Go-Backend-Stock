#!/bin/sh
# Run by the postgres image only when the data volume is empty.
set -eu
psql -v ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v migrator_password="$APP_MIGRATOR_PASSWORD" \
  -v runtime_password="$APP_RUNTIME_PASSWORD" \
  -v maintenance_password="$APP_MAINTENANCE_PASSWORD" \
  -f /bootstrap/bootstrap.sql
