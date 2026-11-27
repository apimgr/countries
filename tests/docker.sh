#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202611231230-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME            :  README.md
# @@Copyright         :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created           :  Sunday, November 22, 2026 02:11 EST
# @@File              :  docker.sh
# @@Description       :  Validate the server and client binaries inside isolated Docker containers.
# @@Changelog         :  Check server API assertions against documented JSON contracts.
# @@TODO              :  None
# @@Other             :  Requires Docker, Make, GNU timeout, and the configured Go build image.
# @@Resource          :  AI.md PARTS 26 and 28
# @@Terminal App      :  no
# @@sudo/root         :  no
# @@Template          :  shell/bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
# shellcheck disable=SC1001,SC1003,SC2001,SC2003,SC2016,SC2031,SC2090,SC2115,SC2120,SC2155,SC2199,SC2229,SC2317,SC2329
# - - - - - - - - - - - - - - - - - - - - - - - - -

set -eo pipefail

APPNAME="${0##*/}"
# shellcheck disable=SC2034
VERSION="202611222004-git"
# shellcheck disable=SC2034
RUN_USER="${USER:-unknown}"
# shellcheck disable=SC2034
SET_UID="${UID:-$(id -u)}"
SCRIPT_SRC_DIR="${BASH_SOURCE[0]%/*}"
DOCKER_TEST_PROJECT_DIR="$(cd "${SCRIPT_SRC_DIR}/.." && pwd)"
DOCKER_TEST_PROJECT_NAME="$(awk -F ': *' '$1 == "project_name" { print $2; exit }' "${DOCKER_TEST_PROJECT_DIR}/IDEA.md")"
DOCKER_TEST_PROJECT_ORG="$(awk -F ': *' '$1 == "project_org" { print $2; exit }' "${DOCKER_TEST_PROJECT_DIR}/IDEA.md")"
DOCKER_TEST_INTERNAL_NAME="$(awk -F ': *' '$1 == "internal_name" { print $2; exit }' "${DOCKER_TEST_PROJECT_DIR}/IDEA.md")"
DOCKER_TEST_ARTIFACT_DIR=""
DOCKER_TEST_CONTAINER_NAME=""
DOCKER_TEST_NETWORK_NAME=""
DOCKER_TEST_BUILD_DIR=""
DOCKER_TEST_ALPINE_IMAGE="alpine:latest"
DOCKER_TEST_KEEP_ARTIFACTS=0
DOCKER_TEST_FAILURES=0
DOCKER_TEST_PORT="$((64000 + RANDOM % 1000))"
DOCKER_TEST_BASE_URL="http://127.0.0.1:${DOCKER_TEST_PORT}"

if (($#)); then
  printf 'Usage: %s\n' "${APPNAME}" >&2
  exit 2
fi

if [[ -z "${NO_COLOR:-}" && -t 1 ]]; then
  DOCKER_TEST_GREEN=$'\033[0;32m'
  DOCKER_TEST_RED=$'\033[0;31m'
  DOCKER_TEST_RESET=$'\033[0m'
else
  DOCKER_TEST_GREEN=""
  DOCKER_TEST_RED=""
  DOCKER_TEST_RESET=""
fi

__say() {
  printf '%s\n' "$*"
}

__fail() {
  printf '%sFAIL:%s %s\n' "${DOCKER_TEST_RED}" "${DOCKER_TEST_RESET}" "$*" >&2
  exit 1
}

__record_failure() {
  DOCKER_TEST_FAILURES=$((DOCKER_TEST_FAILURES + 1))
  printf '%sFAIL:%s %s\n' "${DOCKER_TEST_RED}" "${DOCKER_TEST_RESET}" "$*" >&2
}

__assert_command() {
  local label="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
  else
    __record_failure "${label} command failed."
  fi
}

__cleanup() {
  local exit_status=$?
  if [[ -n "${DOCKER_TEST_CONTAINER_NAME}" ]] && timeout 10s docker container inspect "${DOCKER_TEST_CONTAINER_NAME}" >/dev/null 2>&1; then
    if [[ "${exit_status}" -ne 0 && -n "${DOCKER_TEST_ARTIFACT_DIR}" ]]; then
      timeout 10s docker logs --tail 300 "${DOCKER_TEST_CONTAINER_NAME}" >"${DOCKER_TEST_ARTIFACT_DIR}/container.log" 2>&1 || true
    fi
    if ! timeout 15s docker rm --force "${DOCKER_TEST_CONTAINER_NAME}" >/dev/null 2>&1; then
      __say "Unable to remove test container ${DOCKER_TEST_CONTAINER_NAME}." >&2
      exit_status=1
    fi
  fi
  if [[ -n "${DOCKER_TEST_NETWORK_NAME}" ]] && timeout 10s docker network inspect "${DOCKER_TEST_NETWORK_NAME}" >/dev/null 2>&1; then
    if ! timeout 15s docker network rm "${DOCKER_TEST_NETWORK_NAME}" >/dev/null 2>&1; then
      __say "Unable to remove test network ${DOCKER_TEST_NETWORK_NAME}." >&2
      exit_status=1
    fi
  fi
  if [[ -n "${DOCKER_TEST_ARTIFACT_DIR}" ]]; then
    if [[ "${exit_status}" -eq 0 && "${DOCKER_TEST_KEEP_ARTIFACTS}" -eq 0 ]]; then
      if ! rm -rf -- "${DOCKER_TEST_ARTIFACT_DIR}"; then
        __say "Unable to remove test artifacts at ${DOCKER_TEST_ARTIFACT_DIR}." >&2
        exit_status=1
      fi
    else
      __say "Test artifacts preserved at ${DOCKER_TEST_ARTIFACT_DIR}." >&2
    fi
  fi
  return "${exit_status}"
}

__on_int() {
  exit 130
}

__on_term() {
  exit 143
}

__assert_contains() {
  local label="$1"
  local endpoint="$2"
  local expected="$3"
  local body
  if ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
    __record_failure "${label} request failed (${endpoint})."
    return 0
  fi
  if [[ "${body}" != *"${expected}"* ]]; then
    __record_failure "${label} response did not contain ${expected@Q}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_text_contains() {
  local label="$1"
  local endpoint="$2"
  local expected="$3"
  local body
  if ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- --header='Accept: text/plain' "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
    __record_failure "${label} request failed (${endpoint})."
    return 0
  fi
  if [[ "${body}" != *"${expected}"* ]]; then
    __record_failure "${label} response did not contain ${expected@Q}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_post_contains() {
  local label="$1"
  local endpoint="$2"
  local payload="$3"
  local expected="$4"
  local bearer="${5:-}"
  local mcp_method="${6:-}"
  local body
  if [[ -n "${bearer}" ]]; then
    local -a curl_args=(--header 'Content-Type: application/json' --header "Authorization: Bearer ${bearer}" --data "${payload}")
    if [[ -n "${mcp_method}" ]]; then
      curl_args+=(--header "Mcp-Method: ${mcp_method}")
    fi
    if ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -LSsf "${curl_args[@]}" "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
      __record_failure "${label} request failed (${endpoint})."
      return 0
    fi
  elif ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- --header='Content-Type: application/json' --post-data="${payload}" "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
    __record_failure "${label} request failed (${endpoint})."
    return 0
  fi
  if [[ "${body}" != *"${expected}"* ]]; then
    __record_failure "${label} response did not contain ${expected@Q}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_http_status() {
  local label="$1"
  local expected="$2"
  local status
  shift 2
  if ! status="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -sS -o /dev/null -w '%{http_code}' "$@" 2>/dev/null)"; then
    __record_failure "${label} request failed."
    return 0
  fi
  if [[ "${status}" != "${expected}" ]]; then
    __record_failure "${label} returned HTTP ${status}; expected ${expected}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_content_type() {
  local label="$1"
  local endpoint="$2"
  local accept="$3"
  local expected="$4"
  local headers
  if ! headers="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -sS --dump-header - --output /dev/null --header "Accept: ${accept}" "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
    __record_failure "${label} request failed (${endpoint})."
    return 0
  fi
  if [[ "${headers,,}" != *"content-type: ${expected}"* ]]; then
    __record_failure "${label} did not return Content-Type ${expected}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__test_operator_routes() {
  local route expected query_separator query_url
  DOCKER_TEST_OPERATOR_TOKEN="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" awk -F ': *' '$1 ~ /^[[:space:]]*token$/ { print $2; exit }' /config/secrets.yml)"
  if [[ -z "${DOCKER_TEST_OPERATOR_TOKEN}" ]]; then
    __fail "server.token was not present in the generated secrets.yml."
  fi
  local routes=(
    /debug/config /debug/routes /debug/cache /debug/db /debug/scheduler
    /debug/memory /debug/goroutines /debug/vars /debug/pprof
    /debug/pprof/cmdline /debug/pprof/profile?seconds=1 /debug/pprof/symbol
    /debug/pprof/trace?seconds=1 /debug/pprof/allocs /debug/pprof/block
    /debug/pprof/goroutine /debug/pprof/heap /debug/pprof/mutex
    /debug/pprof/threadcreate /debug/error500
  )
  for route in "${routes[@]}"; do
    expected=200
    [[ "${route}" != /debug/error500 ]] || expected=500
    query_separator='?'
    [[ "${route}" != *\?* ]] || query_separator='&'
    query_url="${DOCKER_TEST_BASE_URL}${route}${query_separator}token=${DOCKER_TEST_OPERATOR_TOKEN}"
    __assert_http_status "operator route ${route}: missing token" 401 "${DOCKER_TEST_BASE_URL}${route}"
    __assert_http_status "operator route ${route}: wrong token" 401 --header 'Authorization: Bearer adm_invalid_test_token' "${DOCKER_TEST_BASE_URL}${route}"
    __assert_http_status "operator route ${route}: owner token" 403 --header "Authorization: Bearer ${DOCKER_TEST_OWNER_TOKEN}" "${DOCKER_TEST_BASE_URL}${route}"
    __assert_http_status "operator route ${route}: valid server token" "${expected}" --header "Authorization: Bearer ${DOCKER_TEST_OPERATOR_TOKEN}" "${DOCKER_TEST_BASE_URL}${route}"
    __assert_http_status "operator route ${route}: query token rejected" 401 "${query_url}"
  done
}

__test_owner_and_cli() {
  local cli_output response_status response_shape response_preview
  if ! response_status="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -sS --output /tmp/owner-token-response.json --write-out '%{http_code}' --header 'Accept: application/json' --header 'Content-Type: application/json' --data '{}' "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences/tokens/primary" 2>/dev/null)" || [[ "${response_status}" != 201 ]]; then
    __fail "The one-time owner-token issue request failed."
  fi
  if ! DOCKER_TEST_OWNER_TOKEN="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" jq -er '.data.owner_token | select(type == "string" and length > 0)' /tmp/owner-token-response.json 2>/dev/null)"; then
    response_shape="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" sh -c 'printf "bytes="; wc -c < /tmp/owner-token-response.json; printf " jq="; jq --version; printf " first_byte="; od -An -tu1 -N1 /tmp/owner-token-response.json; printf " root_fields="; jq -c "keys" /tmp/owner-token-response.json 2>/dev/null || printf invalid; printf " data_fields="; jq -c "(.data // {}) | keys" /tmp/owner-token-response.json 2>/dev/null || printf invalid')"
    response_preview="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" sed -E 's/own_[[:alnum:]_-]+/own_[redacted]/g; s/adm_[[:alnum:]_-]+/adm_[redacted]/g' /tmp/owner-token-response.json)"
    __fail "The owner-token response was not parseable (${response_shape}): ${response_preview}"
  fi
  if ! response_status="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -sS --output /tmp/linked-token-response.json --write-out '%{http_code}' --header 'Accept: application/json' --header 'Content-Type: application/json' --header "Authorization: Bearer ${DOCKER_TEST_OWNER_TOKEN}" --data '{"name":"countries-cli-test","expires":"7d"}' "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences/tokens" 2>/dev/null)" || [[ "${response_status}" != 201 ]]; then
    __fail "The linked CLI token request failed."
  fi
  DOCKER_TEST_LINKED_TOKEN="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" jq -er '.data.linked_token | select(type == "string" and length > 0)' /tmp/linked-token-response.json 2>/dev/null)" || __fail "The linked-token response did not contain a token."
  __say "Checking CLI operations with a linked API token..."
  __assert_cli_contains "CLI list with linked token" "Afghanistan" --token "${DOCKER_TEST_LINKED_TOKEN}" list
  __assert_cli_contains "CLI lookup with linked token" "United States" --token "${DOCKER_TEST_LINKED_TOKEN}" get US
  __assert_cli_contains "CLI search with linked token" "Japan" --token "${DOCKER_TEST_LINKED_TOKEN}" search Japan
  __assert_cli_contains "CLI health request with linked token" '"status"' --token "${DOCKER_TEST_LINKED_TOKEN}" request GET /api/v1/server/healthz
  if ! cli_output="$(timeout 20s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --server "${DOCKER_TEST_BASE_URL}" --token "${DOCKER_TEST_LINKED_TOKEN}" list 2>&1)" || [[ "${cli_output}" != *"Afghanistan"* || "${cli_output}" == *"<html"* ]]; then
    __record_failure "The CLI did not render its default formatted text response."
  else
    printf '%sPASS:%s CLI selects formatted text output\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
  if cli_output="$(timeout 20s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --server "${DOCKER_TEST_BASE_URL}" --token "${DOCKER_TEST_LINKED_TOKEN}" --output json get XX 2>&1)"; then
    __record_failure "The CLI accepted an unknown country code."
  elif [[ "${cli_output,,}" != *"not found"* ]]; then
    __record_failure "The CLI unknown-country error did not explain that the country was not found."
  else
    printf '%sPASS:%s CLI unknown-country failure is clear\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
}

__assert_cli_contains() {
  local label="$1"
  local expected="$2"
  shift 2
  local body
  if ! body="$(timeout 20s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --server "${DOCKER_TEST_BASE_URL}" --output json "$@")"; then
    __record_failure "${label} command failed."
    return 0
  fi
  if [[ "${body}" != *"${expected}"* ]]; then
    __record_failure "${label} output did not contain ${expected@Q}."
    return 0
  fi
  printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__health_core_ready() {
  local response="$1"
  [[ "${response}" == *$'status: healthy'* || "${response}" == *$'status: degraded'* ]] &&
    [[ "${response}" == *$'checks.database: ok'* ]] &&
    [[ "${response}" == *$'checks.cache: ok'* ]] &&
    [[ "${response}" == *$'checks.disk: ok'* ]] &&
    [[ "${response}" == *$'checks.scheduler: ok'* ]]
}

__test_overlay_health() {
  local external_network=0
  local elapsed
  local health=""
  local tor_hostname=""
  local i2p_hostname=""
  local tor_wait="${TOR_WAIT:-180}"
  local i2p_wait="${I2P_WAIT:-600}"
  if [[ ! "${tor_wait}" =~ ^[0-9]+$ || "${tor_wait}" -lt 1 || ! "${i2p_wait}" =~ ^[0-9]+$ || "${i2p_wait}" -lt 1 ]]; then
    __fail "TOR_WAIT and I2P_WAIT must be positive integer seconds."
  fi
  if [[ "${REQUIRE_OVERLAY:-0}" != 0 && "${REQUIRE_OVERLAY:-0}" != 1 ]]; then
    __fail "REQUIRE_OVERLAY must be 0 or 1."
  fi
  if timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -LSsf --connect-timeout 5 --max-time 10 https://www.torproject.org/ >/dev/null 2>&1; then
    external_network=1
  fi
  if [[ "${external_network}" -eq 0 && "${REQUIRE_OVERLAY:-0}" -eq 1 ]]; then
    __record_failure "Overlay bootstrap has no outbound network access and REQUIRE_OVERLAY=1."
    return 0
  fi
  if [[ "${external_network}" -eq 0 ]]; then
    __say "SKIP: overlay bootstrap requires outbound networking (set REQUIRE_OVERLAY=1 for release checks)." >&2
    return
  fi
  __say "Checking Tor health in the binary test container..."
  for ((elapsed = 0; elapsed < tor_wait; elapsed++)); do
    health="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -LSsf --header 'Accept: text/plain' "${DOCKER_TEST_BASE_URL}/api/v1/server/healthz" 2>/dev/null || true)"
    if [[ "${health}" == *$'features.tor: healthy'* ]]; then
      break
    fi
    if [[ "${health}" == *$'features.tor: unavailable'* ]]; then
      __record_failure "Tor became unavailable despite outbound network access."
      break
    fi
    sleep 1
  done
  if [[ "${health}" != *$'features.tor: healthy'* ]]; then
    __record_failure "Tor did not become healthy within ${tor_wait} seconds."
  fi
  tor_hostname="$(printf '%s\n' "${health}" | awk '$1 == "features.tor:" && $2 == "healthy" { for (i = 3; i <= NF; i++) if ($i ~ /\.onion$/) { print $i; exit } }')"
  if [[ ! "${tor_hostname}" =~ ^[a-z2-7]{56}\.onion$ ]]; then
    __record_failure "Tor health did not include a valid v3 onion hostname."
  fi
  if ! timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" sh -c 'test -s /data/tor/site/hostname && test "$(stat -c %a /config/tor)" = 700 && test "$(stat -c %a /data/tor)" = 700 && test "$(stat -c %a /data/tor/site)" = 700'; then
    __record_failure "Tor state was not persisted with the required private directory permissions."
  else
    printf '%sPASS:%s binary Tor health and persisted hostname\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
  __say "Checking I2P health in the binary test container..."
  for ((elapsed = 0; elapsed < i2p_wait; elapsed++)); do
    health="$(timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" curl -q -LSsf --header 'Accept: text/plain' "${DOCKER_TEST_BASE_URL}/api/v1/server/healthz" 2>/dev/null || true)"
    if [[ "${health}" == *$'features.i2p: healthy'* ]]; then
      break
    fi
    sleep 1
  done
  if [[ "${health}" != *$'features.i2p: healthy'* ]]; then
    __record_failure "I2P did not become healthy within ${i2p_wait} seconds."
  fi
  i2p_hostname="$(printf '%s\n' "${health}" | awk '$1 == "features.i2p:" && $2 == "healthy" { for (i = 3; i <= NF; i++) if ($i ~ /^[a-z2-7]+\.b32\.i2p$/) { print $i; exit } }')"
  if [[ ! "${i2p_hostname}" =~ ^[a-z2-7]{52}\.b32\.i2p$ || "${health}" != *'provider=i2pd'* ]]; then
    __record_failure "I2P health did not include the i2pd provider and required b32 hostname."
  fi
  if ! timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" sh -c '! awk '\''$4 == "0A" { split($2, local, ":"); if (local[2] == "1B9E" || local[2] == "115C" || local[2] == "115F" || local[2] == "1DE6") found = 1 } END { exit found ? 0 : 1 }'\'' /proc/net/tcp /proc/net/tcp6'; then
    __record_failure "The managed i2pd process opened a forbidden listener port (7070, 4444, 4447, or 7654)."
  else
    printf '%sPASS:%s binary i2pd has no forbidden listener ports\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
  if ! timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" sh -c 'test -s /data/i2p/site/site-keys.dat && test "$(stat -c %a /config/i2p)" = 700 && test "$(stat -c %a /data/i2p)" = 700 && test "$(stat -c %a /data/i2p/site)" = 700'; then
    __record_failure "I2P destination key was not persisted with the required private directory permissions."
  else
    printf '%sPASS:%s binary I2P health and persisted destination key\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
  if [[ "${health}" == *'/config/'* || "${health}" == *'/data/'* || "${health}" == *'/logs/'* ]]; then
    __record_failure "The health response exposed a filesystem path."
  else
    printf '%sPASS:%s binary health response contains no filesystem paths\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
  fi
}

trap '__cleanup' EXIT
trap '__on_int' INT
trap '__on_term' TERM

if [[ -z "${DOCKER_TEST_PROJECT_NAME}" || -z "${DOCKER_TEST_PROJECT_ORG}" || -z "${DOCKER_TEST_INTERNAL_NAME}" ]]; then
  __fail "IDEA.md must define project_name, project_org, and internal_name."
fi
for DOCKER_TEST_VALUE in "${DOCKER_TEST_PROJECT_NAME}" "${DOCKER_TEST_PROJECT_ORG}" "${DOCKER_TEST_INTERNAL_NAME}"; do
  if [[ ! "${DOCKER_TEST_VALUE}" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
    __fail "Project variables in IDEA.md may contain lowercase letters, digits, underscores, and hyphens only."
  fi
done
if ! command -v docker >/dev/null 2>&1; then
  __say "Docker is required for binary validation." >&2
  exit 69
fi
if ! command -v make >/dev/null 2>&1; then
  __say "Make is required to build the binaries through the project Makefile." >&2
  exit 69
fi
if ! command -v timeout >/dev/null 2>&1; then
  __say "GNU timeout is required to bound container build and request commands." >&2
  exit 69
fi
if ! timeout 15s docker info >/dev/null 2>&1; then
  __say "The Docker daemon is unavailable." >&2
  exit 69
fi
DOCKER_TEST_TEMP_ROOT="${TMPDIR:-/tmp}"
mkdir -p "${DOCKER_TEST_TEMP_ROOT}/${DOCKER_TEST_PROJECT_ORG}"
DOCKER_TEST_ARTIFACT_DIR="$(mktemp -d "${DOCKER_TEST_TEMP_ROOT}/${DOCKER_TEST_PROJECT_ORG}/${DOCKER_TEST_INTERNAL_NAME}-docker-XXXXXX")"
chmod 700 "${DOCKER_TEST_ARTIFACT_DIR}"
DOCKER_TEST_CONTAINER_NAME="${DOCKER_TEST_PROJECT_NAME}-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
DOCKER_TEST_BUILD_DIR="${DOCKER_TEST_PROJECT_DIR}/binaries"
DOCKER_TEST_RELEASE_DIR="${DOCKER_TEST_PROJECT_DIR}/releases"
DOCKER_TEST_CONFIG_DIR="${DOCKER_TEST_ARTIFACT_DIR}/config"
DOCKER_TEST_DATA_DIR="${DOCKER_TEST_ARTIFACT_DIR}/data"
DOCKER_TEST_LOG_DIR="${DOCKER_TEST_ARTIFACT_DIR}/logs"
mkdir -p "${DOCKER_TEST_CONFIG_DIR}" "${DOCKER_TEST_DATA_DIR}" "${DOCKER_TEST_LOG_DIR}"
chmod 700 "${DOCKER_TEST_CONFIG_DIR}" "${DOCKER_TEST_DATA_DIR}" "${DOCKER_TEST_LOG_DIR}"

__say "Building ${DOCKER_TEST_PROJECT_NAME} and its CLI in the required Go container..."
if ! DOCKER_TEST_BUILD_OUTPUT="$(timeout 600s make -C "${DOCKER_TEST_PROJECT_DIR}" BINDIR="${DOCKER_TEST_BUILD_DIR}" RELDIR="${DOCKER_TEST_RELEASE_DIR}" PLATFORMS= build 2>&1)"; then
  __say "${DOCKER_TEST_BUILD_OUTPUT}" >&2
  DOCKER_TEST_KEEP_ARTIFACTS=1
  __fail "The Makefile debug build failed."
fi
__say "${DOCKER_TEST_BUILD_OUTPUT}"
if [[ ! -x "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}" || ! -x "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}-cli" ]]; then
  DOCKER_TEST_KEEP_ARTIFACTS=1
  __fail "The Makefile build did not produce both server and CLI binaries."
fi

DOCKER_TEST_NETWORK_NAME="${DOCKER_TEST_PROJECT_NAME}-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
timeout 15s docker network create --driver bridge "${DOCKER_TEST_NETWORK_NAME}" >/dev/null || __fail "The Docker binary test network could not be created."
timeout 30s docker run --detach --init --name "${DOCKER_TEST_CONTAINER_NAME}" --network "${DOCKER_TEST_NETWORK_NAME}" \
  --memory=1g --cpus=2 --pids-limit=128 --security-opt=no-new-privileges:true \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=64m --tmpfs /run:rw,nosuid,nodev,noexec,size=8m \
  --volume "${DOCKER_TEST_CONFIG_DIR}:/config:rw" \
  --volume "${DOCKER_TEST_DATA_DIR}:/data:rw" \
  --volume "${DOCKER_TEST_LOG_DIR}:/logs:rw" \
  --entrypoint sh "${DOCKER_TEST_ALPINE_IMAGE}" -c 'sleep 1800' >/dev/null
timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" mkdir -p /app || __fail "Unable to create the binary directory in the Alpine container."
timeout 15s docker cp "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}" "${DOCKER_TEST_CONTAINER_NAME}:/app/${DOCKER_TEST_PROJECT_NAME}" || __fail "Unable to copy the server binary into the Alpine container."
timeout 15s docker cp "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}-cli" "${DOCKER_TEST_CONTAINER_NAME}:/app/${DOCKER_TEST_PROJECT_NAME}-cli" || __fail "Unable to copy the CLI binary into the Alpine container."
timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" chmod 0755 "/app/${DOCKER_TEST_PROJECT_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" || __fail "Unable to make copied binaries executable."
__say "Installing Alpine runtime tools in the binary test container..."
if ! timeout 180s docker exec "${DOCKER_TEST_CONTAINER_NAME}" apk add --no-cache curl bash file jq tor i2pd tzdata ca-certificates >/dev/null; then
  __fail "The Alpine binary test container could not install the required test tools."
fi

__say "Starting the server inside the isolated container..."
timeout 15s docker exec --detach "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" \
  --address 0.0.0.0 --port "${DOCKER_TEST_PORT}" --config /config --data /data --log /logs --color no --debug
DOCKER_TEST_HEALTHY=0
DOCKER_TEST_DEADLINE=$((SECONDS + 30))
while ((SECONDS < DOCKER_TEST_DEADLINE)); do
  if timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- "${DOCKER_TEST_BASE_URL}/api/v1/server/healthz" >/dev/null 2>&1; then
    DOCKER_TEST_HEALTHY=1
    break
  fi
  sleep 1
done
if [[ "${DOCKER_TEST_HEALTHY}" -ne 1 ]]; then
  DOCKER_TEST_KEEP_ARTIFACTS=1
  __fail "The server did not become healthy within 30 seconds."
fi

__assert_text_contains "root health alias" "/api/healthz" "status:"
__test_owner_and_cli
__test_operator_routes

__say "Checking server and CLI flags..."
__assert_command "server --help" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --help
__assert_command "server --version" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --version
__assert_command "CLI --help" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --help
__assert_command "CLI --version" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --version
__assert_command "server --status while running" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --port "${DOCKER_TEST_PORT}" --config /config --data /data --log /logs --status

__say "Checking binary metadata and rename behavior..."
if ! DOCKER_TEST_FILE_INFO="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" file "/app/${DOCKER_TEST_PROJECT_NAME}")" || [[ "${DOCKER_TEST_FILE_INFO}" != *"ELF"* ]]; then
  __record_failure "The server binary metadata was not a Linux executable."
else
  printf '%sPASS:%s server binary metadata is Linux ELF\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
fi
__assert_command "CLI binary metadata" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" file "/app/${DOCKER_TEST_PROJECT_NAME}-cli"
__assert_command "copy server binary for rename check" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" cp "/app/${DOCKER_TEST_PROJECT_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-renamed-server"
if ! DOCKER_TEST_RENAMED_HELP="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-renamed-server" --help 2>&1)" || [[ "${DOCKER_TEST_RENAMED_HELP}" != *"${DOCKER_TEST_PROJECT_NAME}-renamed-server"* ]]; then
  __record_failure "The renamed server binary did not use its executable name in help."
else
  printf '%sPASS:%s server binary rename behavior\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
fi
__assert_command "copy CLI binary for rename check" timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" cp "/app/${DOCKER_TEST_PROJECT_NAME}-cli" "/app/${DOCKER_TEST_PROJECT_NAME}-cli-renamed"
if ! DOCKER_TEST_RENAMED_HELP="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli-renamed" --help 2>&1)" || [[ "${DOCKER_TEST_RENAMED_HELP}" != *"${DOCKER_TEST_PROJECT_NAME}-cli-renamed"* ]]; then
  __record_failure "The renamed client binary did not use its executable name in help."
else
  printf '%sPASS:%s CLI binary rename behavior\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
fi

__say "Checking web pages, REST, GraphQL, discovery, and text endpoints..."
__assert_contains "home SSR page" "/" "Countries"
__assert_contains "search SSR page" "/search?q=Japan" "Japan"
__assert_contains "coordinate SSR page" "/coordinates?lat=40.7128&lon=-74.0060" "Bermuda"
__assert_contains "server root redirect" "/server" "Countries"
__assert_content_type "browser HTML content negotiation" "/" "text/html" "text/html"
__assert_contains "about server page" "/server/about" "Countries"
__assert_contains "DB-IP attribution on about page" "/server/about" '<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>'
__assert_contains "help server page" "/server/help" "Countries"
__assert_contains "MCP help server page" "/server/help/mcp" "Endpoint URL"
__assert_text_contains "health server page" "/server/healthz" "Status:"
__assert_contains "privacy server page" "/server/privacy" "Privacy"
__assert_contains "terms server page" "/server/terms" "Terms"
__assert_contains "security server page" "/server/security" "Security"
__assert_contains "security policy server page" "/server/security/policy" "Scope"
__assert_contains "security thanks server page" "/server/security/thanks" "Acknowledged researchers"
__assert_contains "contact server page" "/server/contact" "Contact"
__assert_contains "DPO server page" "/server/dpo" "Data Protection"
__assert_contains "preferences server page" "/server/preferences" "Appearance"
__assert_contains "preferences sync page" "/server/preferences/sync" "sync"
__assert_contains "OpenAPI browser page" "/server/docs/swagger" "OpenAPI"
__assert_contains "GraphQL browser page" "/server/docs/graphql" "GraphQL"
__assert_contains "country lookup API" "/api/v1/countries/US" "United States"
__assert_contains "country search API" "/api/v1/countries/search?q=Japan" "Japan"
__assert_contains "country region filter API" "/api/v1/countries?region=Asia&limit=1" "Asia"
__assert_contains "nearest country API" "/api/v1/coordinates?lat=40.7128&lon=-74.0060" "Bermuda"
__assert_contains "nearby country API" "/api/v1/nearby?lat=38&lon=-97&radius=250" "United States"
__assert_contains "dataset stats API" "/api/v1/stats" "total_countries"
__assert_contains "dataset count API" "/api/v1/count" "count"
__assert_text_contains "dataset stats text API" "/api/v1/stats.txt" "Total Countries: 249"
__assert_text_contains "dataset count text API" "/api/v1/count.txt" "249"
__assert_text_contains "versioned health API" "/api/v1/server/healthz" "status:"
__assert_text_contains "versioned health text API" "/api/v1/server/healthz.txt" "status:"
__assert_content_type "API JSON content negotiation" "/api/v1/countries/US" "application/json" "application/json"
__assert_content_type "API plain-text content negotiation" "/api/v1/countries/US" "text/plain" "text/plain"
__assert_contains "about API mirror" "/api/v1/server/about" "Features"
__assert_contains "help API mirror" "/api/v1/server/help" "Find countries"
__assert_contains "MCP help API mirror" "/api/v1/server/help/mcp" "getCountries"
__assert_contains "privacy API mirror" "/api/v1/server/privacy" "data_stored_on_server"
__assert_contains "terms API mirror" "/api/v1/server/terms" "Terms"
__assert_contains "security API mirror" "/api/v1/server/security" "contacts"
__assert_contains "security policy API mirror" "/api/v1/server/security/policy" "Scope"
__assert_contains "security thanks API mirror" "/api/v1/server/security/thanks" "entries"
__assert_contains "contact API mirror" "/api/v1/server/contact" "types"
__assert_contains "DPO API mirror" "/api/v1/server/dpo" "privacy_url"
__assert_contains "preferences API" "/api/v1/server/preferences" "theme"
__assert_contains "preferences sync API" "/api/v1/server/preferences/sync" "code"
__assert_contains "preferences sync import API" "/api/v1/server/preferences/sync/import" "theme"
__assert_http_status "token listing requires primary owner token" 401 "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences/tokens"
__assert_http_status "primary owner token lists tokens" 200 --header "Authorization: Bearer ${DOCKER_TEST_OWNER_TOKEN}" "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences/tokens"
__assert_http_status "preferences API rejects malformed JSON" 400 --header 'Content-Type: application/json' --data '{' "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences"
__assert_http_status "preferences sync API rejects malformed JSON" 400 --header 'Content-Type: application/json' --data '{' "${DOCKER_TEST_BASE_URL}/api/v1/server/preferences/sync"
__assert_http_status "contact API rejects malformed JSON" 400 --header 'Content-Type: application/json' --data '{' "${DOCKER_TEST_BASE_URL}/api/v1/server/contact"
__assert_http_status "report API rejects unsupported content type" 415 --header 'Content-Type: application/json' --data '{}' "${DOCKER_TEST_BASE_URL}/api/v1/server/reports/csp"
__assert_http_status "report API rejects unknown report type" 404 --header 'Content-Type: application/csp-report' --data '{}' "${DOCKER_TEST_BASE_URL}/api/v1/server/reports/unknown"
__assert_contains "versioned API root" "/api/v1" "countries"
__assert_contains "Swagger JSON API" "/api/v1/server/swagger" "openapi"
__assert_contains "well-known security.txt" "/.well-known/security.txt" "Policy:"
__assert_contains "llms.txt discovery" "/llms.txt" "Countries"
__assert_text_contains "robots.txt discovery" "/robots.txt" "User-agent: *"
__assert_text_contains "robots sitemap declaration" "/robots.txt" "Sitemap:"
__assert_contains "manifest discovery" "/manifest.json" "name"
__assert_contains "service worker asset" "/sw.js" "CACHE_NAME"
__assert_contains "common stylesheet asset" "/static/css/common.css" "--color-bg-primary"
__assert_contains "shared components stylesheet asset" "/static/css/components.css" ".btn-primary"
__assert_contains "public stylesheet asset" "/static/css/public.css" ".header"
__assert_contains "main JavaScript asset" "/static/js/app.js" "addEventListener"
__assert_contains "sitemap discovery" "/sitemap.xml" "urlset"
__assert_contains "Swagger API alias" "/api/swagger" "openapi"
__assert_contains "GraphQL countries GET query" "/api/v1/server/graphql?query=%7B%20countries%20%7B%20name%20%7B%20common%20%7D%20%7D%20%7D" "Japan"
__assert_post_contains "GraphQL countries API" "/api/v1/server/graphql" '{"query":"{ countries { name { common } } }"}' "Japan"
__assert_post_contains "GraphQL alias" "/api/graphql" '{"query":"{ countries { name { common } } }"}' "Japan"
__assert_post_contains "nearest country POST API" "/api/v1/coordinates" '{"lat":40.7128,"lon":-74.0060}' "Bermuda"
__assert_post_contains "MCP tool discovery" "/api/v1/server/mcp" '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}' '"name":"getcountries"' "${DOCKER_TEST_LINKED_TOKEN}" "tools/list"

__assert_cli_contains "CLI list" "Afghanistan" list
__assert_cli_contains "CLI country lookup" "United States" get US
__assert_cli_contains "CLI search" "Japan" search Japan
__assert_cli_contains "CLI nearest-coordinate lookup" "Bermuda" nearest 40.7128 -74.0060
__assert_cli_contains "CLI nearby-country lookup" "United States" nearby 38 -97 250
__assert_cli_contains "CLI stats" "total_countries" stats

if timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- "${DOCKER_TEST_BASE_URL}/not-a-real-page" >/dev/null 2>&1; then
  __record_failure "An unknown page unexpectedly returned a successful response."
else
  printf '%sPASS:%s unknown page returns an error\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
fi

__test_overlay_health

if [[ "${DOCKER_TEST_FAILURES}" -gt 0 ]]; then
  __say "Docker binary validation failed: ${DOCKER_TEST_FAILURES} check(s) failed." >&2
  exit 1
fi
__say "Docker binary validation passed."

# ex: ts=4 sw=4 et filetype=sh
