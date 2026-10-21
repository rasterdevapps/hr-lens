#!/usr/bin/env bash
# Builds a deployable HR Lens bundle: the Linux server binary, the built Angular app, the browser
# extension and agent release files. Run it from the backend folder on a machine with Go and Node.
#
#   ./deploy/build-bundle.sh
#
# Produces deploy/out/ and deploy/hrlens-deploy.tar.gz to copy to the office server.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"   # the backend folder
ROOT="$(cd "$HERE/.." && pwd)"             # the project root
OUT="$HERE/deploy/out"

echo "==> Building the Angular app (production)"
( cd "$ROOT/screen-management" && npx ng build --configuration production )

echo "==> Building the server (linux/amd64)"
( cd "$HERE" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT/hrlens-server" ./cmd/server )

echo "==> Assembling the bundle"
rm -rf "$OUT/web" "$OUT/dist"
mkdir -p "$OUT/web" "$OUT/dist"
cp -r "$ROOT/screen-management/dist/screen-management/browser/." "$OUT/web/"
# Served files the server needs: the extension and the signed agent releases + download package.
[ -d "$HERE/dist/extension" ] && cp -r "$HERE/dist/extension" "$OUT/dist/extension"
[ -d "$HERE/dist/agent" ]     && cp -r "$HERE/dist/agent"     "$OUT/dist/agent"
[ -d "$HERE/dist/hrlens-agent-linux" ] && cp -r "$HERE/dist/hrlens-agent-linux" "$OUT/dist/hrlens-agent-linux"
[ -f "$HERE/dist/hrlens-agent-linux.tar.gz" ] && cp "$HERE/dist/hrlens-agent-linux.tar.gz" "$OUT/dist/"
cp "$HERE/deploy/.env.production.example" "$OUT/.env.example"
cp "$HERE/deploy/hrlens.service" "$OUT/hrlens.service"
cp "$HERE/deploy/Caddyfile" "$OUT/Caddyfile"
cp "$HERE/deploy/DEPLOY.md" "$OUT/DEPLOY.md"

echo "==> Packing deploy/hrlens-deploy.tar.gz"
tar -C "$HERE/deploy" -czf "$HERE/deploy/hrlens-deploy.tar.gz" out

echo "Done. Copy deploy/hrlens-deploy.tar.gz to the server and follow DEPLOY.md."
