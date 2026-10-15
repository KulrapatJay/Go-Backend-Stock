#!/usr/bin/env bash
# Downloads Swagger UI from the npm registry, verifies its integrity hash and copies the
# files the API embeds. The copied files are committed; CI does not re-download them.
set -euo pipefail

VERSION="5.33.1"
DEST="internal/adapter/http/assets/swagger-ui"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

expected="$(curl -fsSL "https://registry.npmjs.org/swagger-ui-dist/${VERSION}" \
  | python3 -c 'import json, sys; print(json.load(sys.stdin)["dist"]["integrity"])')"
curl -fsSL -o "$WORK/package.tgz" "https://registry.npmjs.org/swagger-ui-dist/-/swagger-ui-dist-${VERSION}.tgz"
actual="sha512-$(openssl dgst -sha512 -binary "$WORK/package.tgz" | openssl base64 -A)"
if [ "$expected" != "$actual" ]; then
  echo "integrity mismatch: expected $expected got $actual" >&2
  exit 1
fi

mkdir -p "$WORK/extract" "$DEST"
tar -xzf "$WORK/package.tgz" -C "$WORK/extract"
for file in swagger-ui.css swagger-ui-bundle.js swagger-ui-standalone-preset.js favicon-32x32.png LICENSE NOTICE; do
  cp "$WORK/extract/package/$file" "$DEST/$file"
done
echo "$VERSION" > "$DEST/VERSION"
echo "Swagger UI ${VERSION} copied to ${DEST}"
