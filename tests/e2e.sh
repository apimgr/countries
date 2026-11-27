#!/usr/bin/env bash
# @@License : WTFPL
set -eo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
project_org="$(awk -F': *' '$1 == "project_org" { print $2; exit }' "$project_dir/IDEA.md")"
internal_name="$(awk -F': *' '$1 == "internal_name" { print $2; exit }' "$project_dir/IDEA.md")"
if [[ -z "$project_org" || -z "$internal_name" ]]; then
  printf 'IDEA.md must define project_org and internal_name for E2E artifacts.\n' >&2
  exit 1
fi
artifact_parent="${TMPDIR:-/tmp}/${project_org}"
mkdir -p "$artifact_parent"
artifact_dir="$(mktemp -d "$artifact_parent/${internal_name}-e2e-XXXXXX")"
chmod 700 "$artifact_dir"
network_name="countries-e2e-$(basename "$artifact_dir" | tr -cd '[:alnum:]')"
browser_name="countries-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
server_name="countries-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
keep_artifacts=0

cleanup() {
  status=$?
  if docker container inspect "$browser_name" >/dev/null 2>&1; then
    docker rm -f "$browser_name" >/dev/null 2>&1 || true
  fi
  docker network rm "$network_name" >/dev/null 2>&1 || true
  if [[ "$status" -eq 0 && "$keep_artifacts" -eq 0 ]]; then
    rm -rf -- "$artifact_dir"
  else
    printf 'E2E artifacts preserved at %s\n' "$artifact_dir" >&2
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! command -v docker >/dev/null 2>&1; then
  printf 'Docker is required for the browser E2E suite.\n' >&2
  exit 1
fi
if ! docker image inspect casjaysdev/go:latest >/dev/null 2>&1; then
  printf 'Required Go container image casjaysdev/go:latest is not available locally.\n' >&2
  exit 1
fi
if ! docker image inspect chromedp/headless-shell:latest >/dev/null 2>&1; then
  printf 'Required browser image chromedp/headless-shell:latest is not available locally.\n' >&2
  exit 1
fi

port=""
for candidate in $(seq 64000 64999 | shuf); do
  if python3 -c 'import socket, sys; s=socket.socket(); s.bind(("", int(sys.argv[1]))); s.close()' "$candidate" 2>/dev/null; then
    port="$candidate"
    break
  fi
done
if [[ -z "$port" ]]; then
  printf 'No available port in the required 64000–64999 range.\n' >&2
  exit 1
fi
module_cache="$artifact_dir/module-cache"
build_cache="$artifact_dir/build-cache"
tmp_dir="$artifact_dir/tmp"
mkdir -p "$module_cache" "$build_cache" "$tmp_dir"
chown -R 65534:65534 "$artifact_dir"

download_name="countries-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
docker run --rm \
  --name "$download_name" \
  --entrypoint go \
  --user 65534:65534 \
  -v "$project_dir:/workspace:ro" \
  -v "$module_cache:/usr/local/share/go/pkg/mod" \
  -v "$build_cache:/usr/local/share/go/cache" \
  -v "$tmp_dir:/artifacts/tmp" \
  -w /workspace \
  -e GOCACHE=/usr/local/share/go/cache \
  -e TMPDIR=/artifacts/tmp \
  -e GOFLAGS=-buildvcs=false \
  -e CGO_ENABLED=0 \
  casjaysdev/go:latest mod download

docker network create --internal "$network_name" >/dev/null
docker run --rm -d \
  --network "$network_name" \
  --network-alias countries-e2e-browser \
  --name "$browser_name" \
  chromedp/headless-shell:latest >/dev/null

set +e
docker run --rm \
  --name "$server_name" \
  --entrypoint sh \
  --network "$network_name" \
  --network-alias countries-e2e-server \
  --user 65534:65534 \
  -v "$project_dir:/workspace" \
  -v "$artifact_dir:/artifacts" \
  -v "$module_cache:/usr/local/share/go/pkg/mod" \
  -v "$build_cache:/usr/local/share/go/cache" \
  -w /workspace \
  -e GOCACHE=/usr/local/share/go/cache \
  -e TMPDIR=/artifacts/tmp \
  -e GOFLAGS=-buildvcs=false \
  -e CGO_ENABLED=0 \
  -e COUNTRIES_E2E_TMPDIR=/artifacts \
  -e COUNTRIES_E2E_PORT="$port" \
  -e COUNTRIES_E2E_BASE_URL="http://countries-e2e-server:$port" \
  -e COUNTRIES_E2E_BINARY=/artifacts/countries \
  -e CHROMEDP_REMOTE_WS=ws://countries-e2e-browser:9222/ \
  casjaysdev/go:latest \
  -c 'go build -trimpath -o /artifacts/countries ./src && go test -tags e2e ./tests/e2e/...'
status=$?
set -e
if [[ "$status" -ne 0 ]]; then
  keep_artifacts=1
  exit "$status"
fi
