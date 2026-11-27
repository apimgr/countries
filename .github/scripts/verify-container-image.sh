#!/usr/bin/env bash
set -Eeuo pipefail

image=${1:?usage: verify-container-image.sh IMAGE}
container="countries-image-check-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
health=''
healthy=false

trap 'docker rm --force "$container" >/dev/null 2>&1 || true' EXIT

docker run --rm --entrypoint sh "$image" -c 'for command in bash curl tor i2pd tini; do command -v "$command" >/dev/null || exit 1; done'
docker run --detach --name "$container" "$image" >/dev/null

deadline=$(( $(date +%s) + 180 ))
while (( $(date +%s) < deadline )); do
  health=$(docker exec "$container" curl -q -LSs --max-time 5 --header 'Accept: application/json' http://127.0.0.1/api/v1/server/healthz 2>/dev/null || true)
  tor_status=$(sed -nE 's/.*"features"[[:space:]]*:[[:space:]]*\{.*"tor"[[:space:]]*:[[:space:]]*\{[^}]*"status"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' <<<"${health//$'\n'/}")
  if [[ "$tor_status" == healthy ]]; then
    healthy=true
    break
  fi
  if [[ "$tor_status" == error ]]; then
    break
  fi
  sleep 5
done

if [[ "$healthy" == true ]]; then
  exit 0
fi

docker logs "$container" >&2 || true
if [[ "$tor_status" == starting || "$tor_status" == unavailable ]]; then
  if ! docker exec "$container" curl -q -LSsf --connect-timeout 5 --max-time 10 https://check.torproject.org/ >/dev/null 2>&1; then
    if [[ "${REQUIRE_OVERLAY:-0}" != 1 ]]; then
      echo 'SKIP: no outbound network' >&2
      exit 0
    fi
  fi
fi

echo "Tor did not become healthy in $image within 180 seconds" >&2
exit 1
