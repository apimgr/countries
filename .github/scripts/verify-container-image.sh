#!/usr/bin/env bash
set -Eeuo pipefail

image=${1:?usage: verify-container-image.sh IMAGE}
container="countries-image-check-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)"
health=''
healthy=false

cleanup() {
  if [[ -n "$container" ]]; then
    docker rm --force "$container" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker run --rm --entrypoint sh "$image" -c 'for command in bash curl tor i2pd tini; do command -v "$command" >/dev/null || exit 1; done'
docker run --detach --name "$container" "$image" >/dev/null

deadline=$(( $(date +%s) + 180 ))
while (( $(date +%s) < deadline )); do
  health=$(docker exec "$container" curl -q -LSs --max-time 5 --header 'Accept: text/plain' http://127.0.0.1/server/healthz 2>/dev/null || true)
  if grep -Fq 'features.tor: healthy' <<<"$health"; then
    healthy=true
    break
  fi
  if grep -Eq 'features\.tor: error([[:space:]]|$)' <<<"$health"; then
    break
  fi
  sleep 5
done

if [[ "$healthy" == true ]]; then
  exit 0
fi

docker logs "$container" >&2 || true
if grep -Eq 'features\.tor: (starting|unavailable)([[:space:]]|$)' <<<"$health"; then
  if ! docker exec "$container" curl -q -LSsf --connect-timeout 5 --max-time 10 https://check.torproject.org/ >/dev/null 2>&1; then
    if [[ "${REQUIRE_OVERLAY:-0}" != 1 ]]; then
      echo 'SKIP: no outbound network' >&2
      exit 0
    fi
  fi
fi

echo "Tor did not become healthy in $image within 180 seconds" >&2
exit 1
