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
DOCKER_TEST_IMAGE_CONTAINER_NAME=""
DOCKER_TEST_NETWORK_NAME=""
DOCKER_TEST_IMAGE=""
DOCKER_TEST_BUILD_DIR=""
DOCKER_TEST_KEEP_ARTIFACTS=0
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

__cleanup() {
	local exit_status=$?
	if [[ -n "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" ]] && timeout 10s docker container inspect "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" >/dev/null 2>&1; then
		if [[ "${exit_status}" -ne 0 && -n "${DOCKER_TEST_ARTIFACT_DIR}" ]]; then
			timeout 10s docker logs --tail 300 "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" >"${DOCKER_TEST_ARTIFACT_DIR}/image-container.log" 2>&1 || true
		fi
		if ! timeout 15s docker rm --force "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" >/dev/null 2>&1; then
			__say "Unable to remove test image container ${DOCKER_TEST_IMAGE_CONTAINER_NAME}." >&2
			exit_status=1
		fi
	fi
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
	if [[ -n "${DOCKER_TEST_IMAGE}" ]] && timeout 10s docker image inspect "${DOCKER_TEST_IMAGE}" >/dev/null 2>&1; then
		if ! timeout 30s docker image rm "${DOCKER_TEST_IMAGE}" >/dev/null 2>&1; then
			__say "Unable to remove test image ${DOCKER_TEST_IMAGE}." >&2
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
		__fail "${label} request failed (${endpoint})."
	fi
	if [[ "${body}" != *"${expected}"* ]]; then
		__fail "${label} response did not contain ${expected@Q}."
	fi
	printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_text_contains() {
	local label="$1"
	local endpoint="$2"
	local expected="$3"
	local body
	if ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- --header='Accept: text/plain' "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
		__fail "${label} request failed (${endpoint})."
	fi
	if [[ "${body}" != *"${expected}"* ]]; then
		__fail "${label} response did not contain ${expected@Q}."
	fi
	printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_post_contains() {
	local label="$1"
	local endpoint="$2"
	local payload="$3"
	local expected="$4"
	local body
	if ! body="$(timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- --header='Content-Type: application/json' --post-data="${payload}" "${DOCKER_TEST_BASE_URL}${endpoint}" 2>/dev/null)"; then
		__fail "${label} request failed (${endpoint})."
	fi
	if [[ "${body}" != *"${expected}"* ]]; then
		__fail "${label} response did not contain ${expected@Q}."
	fi
	printf '%sPASS:%s %s\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}" "${label}"
}

__assert_cli_contains() {
	local label="$1"
	local expected="$2"
	shift 2
	local body
	if ! body="$(timeout 20s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --server "${DOCKER_TEST_BASE_URL}" --output json "$@")"; then
		__fail "${label} command failed."
	fi
	if [[ "${body}" != *"${expected}"* ]]; then
		__fail "${label} output did not contain ${expected@Q}."
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

__test_shipped_image() {
	local official_site=""
	local external_network=0
	local tor_wait="${TOR_WAIT:-180}"
	local i2p_wait="${I2P_WAIT:-600}"
	local elapsed
	local healthy=0
	local health=""
	local tor_hostname=""
	local i2p_hostname=""
	if [[ -s "${DOCKER_TEST_PROJECT_DIR}/site.txt" ]]; then
		official_site="$(<"${DOCKER_TEST_PROJECT_DIR}/site.txt")"
	fi
	if [[ ! "${tor_wait}" =~ ^[0-9]+$ || "${tor_wait}" -lt 1 || ! "${i2p_wait}" =~ ^[0-9]+$ || "${i2p_wait}" -lt 1 ]]; then
		__fail "TOR_WAIT and I2P_WAIT must be positive integer seconds."
	fi
	if [[ "${REQUIRE_OVERLAY:-0}" != 0 && "${REQUIRE_OVERLAY:-0}" != 1 ]]; then
		__fail "REQUIRE_OVERLAY must be 0 or 1."
	fi
	DOCKER_TEST_NETWORK_NAME="${DOCKER_TEST_INTERNAL_NAME}-docker-net-$(basename "${DOCKER_TEST_ARTIFACT_DIR}" | tr -cd '[:alnum:]')"
	DOCKER_TEST_IMAGE_CONTAINER_NAME="${DOCKER_TEST_PROJECT_NAME}-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
	DOCKER_TEST_IMAGE="${DOCKER_TEST_PROJECT_NAME}:docker-test-$(basename "${DOCKER_TEST_ARTIFACT_DIR}" | tr -cd '[:alnum:]')"
	local image_config="${DOCKER_TEST_ARTIFACT_DIR}/image-config"
	local image_data="${DOCKER_TEST_ARTIFACT_DIR}/image-data"
	local image_logs="${DOCKER_TEST_ARTIFACT_DIR}/image-logs"
	mkdir -p "${image_config}" "${image_data}" "${image_logs}"
	chmod 755 "${image_config}" "${image_data}" "${image_logs}"
	__say "Building and inspecting the shipped production image..."
	if ! timeout 600s docker build --build-arg "OFFICIAL_SITE=${official_site}" --file "${DOCKER_TEST_PROJECT_DIR}/docker/Dockerfile" --tag "${DOCKER_TEST_IMAGE}" "${DOCKER_TEST_PROJECT_DIR}"; then
		DOCKER_TEST_KEEP_ARTIFACTS=1
		__fail "The production Docker image build failed."
	fi
	local runtime_check=""
	if ! runtime_check="$(timeout 30s docker run --rm --entrypoint sh "${DOCKER_TEST_IMAGE}" -c 'for command in bash curl tor i2pd tini groupadd useradd; do command -v "$command" >/dev/null || { printf "missing runtime command: %s\\n" "$command" >&2; exit 1; }; done' 2>&1)"; then
		__fail "The production image runtime check failed: ${runtime_check}"
	fi
	if ! timeout 15s docker network create --driver bridge "${DOCKER_TEST_NETWORK_NAME}" >/dev/null; then
		__fail "The Docker image test network could not be created."
	fi
	timeout 30s docker run --detach --init --name "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" \
		--network "${DOCKER_TEST_NETWORK_NAME}" \
		--memory=1g --cpus=2 --pids-limit=128 --security-opt=no-new-privileges:true \
		--tmpfs /tmp:rw,nosuid,nodev,noexec,size=64m --tmpfs /run:rw,nosuid,nodev,noexec,size=16m \
		--volume "${image_config}:/config:rw" \
		--volume "${image_data}:/data:rw" \
		--volume "${image_logs}:/logs:rw" \
		"${DOCKER_TEST_IMAGE}" >/dev/null
	__say "Waiting for the shipped image health endpoint..."
	local deadline=$((SECONDS + 90))
	while (( SECONDS < deadline )); do
		health="$(timeout 10s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" curl -q -LSsf --header 'Accept: text/plain' http://127.0.0.1/api/v1/server/healthz 2>/dev/null || true)"
		if __health_core_ready "${health}"; then
			healthy=1
			break
		fi
		sleep 1
	done
	if [[ "${healthy}" -ne 1 ]]; then
		DOCKER_TEST_KEEP_ARTIFACTS=1
		__fail "The production image server did not become healthy within 90 seconds."
	fi
	printf '%sPASS:%s production image server starts\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
	if timeout 15s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" curl -q -LSsf --connect-timeout 5 --max-time 10 https://www.torproject.org/ >/dev/null 2>&1; then
		external_network=1
	fi
	if [[ "${external_network}" -eq 0 && "${REQUIRE_OVERLAY:-0}" -eq 1 ]]; then
		__fail "Overlay bootstrap has no outbound network access and REQUIRE_OVERLAY=1."
	fi
	if [[ "${external_network}" -eq 1 ]]; then
		for ((elapsed = 0; elapsed < tor_wait; elapsed++)); do
		health="$(timeout 10s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" curl -q -LSsf --header 'Accept: text/plain' http://127.0.0.1/api/v1/server/healthz 2>/dev/null || true)"
			if [[ "${health}" == *$'features.tor: healthy'* ]]; then
				break
			fi
			if [[ "${health}" == *$'features.tor: unavailable'* ]]; then
				__fail "Tor became unavailable in the production image despite outbound network access."
			fi
			sleep 1
		done
		if [[ "${health}" != *$'features.tor: healthy'* ]]; then
			__fail "Tor did not become healthy within ${tor_wait} seconds."
		fi
		tor_hostname="$(printf '%s\n' "${health}" | awk '$1 == "features.tor:" && $2 == "healthy" { for (i = 3; i <= NF; i++) if ($i ~ /\.onion$/) { print $i; exit } }')"
		if [[ ! "${tor_hostname}" =~ ^[a-z2-7]{56}\.onion$ ]]; then
			__fail "Tor health did not include a valid v3 onion hostname."
		fi
		if ! timeout 15s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" sh -c 'test -s /data/countries/tor/site/hostname && test "$(stat -c %a /config/countries/tor)" = 700 && test "$(stat -c %a /data/countries/tor)" = 700 && test "$(stat -c %a /data/countries/tor/site)" = 700'; then
			__fail "Tor state was not persisted with the required private directory permissions."
		fi
		printf '%sPASS:%s production image Tor health and persisted hostname\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
	else
		__say "SKIP: overlay bootstrap requires outbound networking (set REQUIRE_OVERLAY=1 for release checks)."
	fi
	if [[ "${external_network}" -eq 1 ]]; then
		for ((elapsed = 0; elapsed < i2p_wait; elapsed++)); do
			health="$(timeout 10s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" curl -q -LSsf --header 'Accept: text/plain' http://127.0.0.1/api/v1/server/healthz 2>/dev/null || true)"
			if [[ "${health}" == *$'features.i2p: healthy'* ]]; then
				break
			fi
			sleep 1
		done
		if [[ "${health}" != *$'features.i2p: healthy'* ]]; then
			__fail "I2P did not become healthy within ${i2p_wait} seconds."
		fi
		i2p_hostname="$(printf '%s\n' "${health}" | awk '$1 == "features.i2p:" && $2 == "healthy" { for (i = 3; i <= NF; i++) if ($i ~ /^[a-z2-7]+\.b32\.i2p$/) { print $i; exit } }')"
		if [[ ! "${i2p_hostname}" =~ ^[a-z2-7]{52}\.b32\.i2p$ || "${health}" != *'provider=i2pd'* ]]; then
			__fail "I2P health did not include the i2pd provider and required b32 hostname."
		fi
		if ! timeout 15s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" sh -c '! awk '\''$4 == "0A" { split($2, local, ":"); if (local[2] == "1B9E" || local[2] == "115C" || local[2] == "115F" || local[2] == "1DE6") found = 1 } END { exit found ? 0 : 1 }'\'' /proc/net/tcp /proc/net/tcp6'; then
			__fail "The managed i2pd process opened a forbidden listener port (7070, 4444, 4447, or 7654)."
		fi
		if ! timeout 15s docker exec "${DOCKER_TEST_IMAGE_CONTAINER_NAME}" sh -c 'test -s /data/countries/i2p/site/site-keys.dat && test "$(stat -c %a /config/countries/i2p)" = 700 && test "$(stat -c %a /data/countries/i2p)" = 700 && test "$(stat -c %a /data/countries/i2p/site)" = 700'; then
			__fail "I2P destination key was not persisted with the required private directory permissions."
		fi
		printf '%sPASS:%s production image i2pd has no forbidden listener ports\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
		printf '%sPASS:%s production image I2P health and persisted destination key\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
	fi
	if [[ "${health}" == *'/config/'* || "${health}" == *'/data/'* || "${health}" == *'/logs/'* ]]; then
		__fail "The health response exposed a filesystem path."
	fi
	printf '%sPASS:%s production image health response contains no filesystem paths\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"
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
	__fail "Docker is required for binary validation."
fi
if ! command -v make >/dev/null 2>&1; then
	__fail "Make is required to build the binaries through the project Makefile."
fi
if ! command -v timeout >/dev/null 2>&1; then
	__fail "GNU timeout is required to bound container build and request commands."
fi
if ! timeout 15s docker info >/dev/null 2>&1; then
	__fail "The Docker daemon is unavailable."
fi
DOCKER_TEST_TEMP_ROOT="${TMPDIR:-/tmp}"
mkdir -p "${DOCKER_TEST_TEMP_ROOT}/${DOCKER_TEST_PROJECT_ORG}"
DOCKER_TEST_ARTIFACT_DIR="$(mktemp -d "${DOCKER_TEST_TEMP_ROOT}/${DOCKER_TEST_PROJECT_ORG}/${DOCKER_TEST_INTERNAL_NAME}-docker-XXXXXX")"
chmod 700 "${DOCKER_TEST_ARTIFACT_DIR}"
DOCKER_TEST_CONTAINER_NAME="${DOCKER_TEST_PROJECT_NAME}-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
DOCKER_TEST_BUILD_ROOT="${DOCKER_TEST_ARTIFACT_DIR}/build-root"
DOCKER_TEST_CONFIG_DIR="${DOCKER_TEST_ARTIFACT_DIR}/config"
DOCKER_TEST_DATA_DIR="${DOCKER_TEST_ARTIFACT_DIR}/data"
DOCKER_TEST_LOG_DIR="${DOCKER_TEST_ARTIFACT_DIR}/logs"
mkdir -p "${DOCKER_TEST_BUILD_ROOT}" "${DOCKER_TEST_CONFIG_DIR}" "${DOCKER_TEST_DATA_DIR}" "${DOCKER_TEST_LOG_DIR}"
chmod 700 "${DOCKER_TEST_BUILD_ROOT}" "${DOCKER_TEST_CONFIG_DIR}" "${DOCKER_TEST_DATA_DIR}" "${DOCKER_TEST_LOG_DIR}"

__say "Building ${DOCKER_TEST_PROJECT_NAME} and its CLI in the required Go container..."
if ! DOCKER_TEST_BUILD_OUTPUT="$(timeout 600s env TMPDIR="${DOCKER_TEST_BUILD_ROOT}" make -C "${DOCKER_TEST_PROJECT_DIR}" dev 2>&1)"; then
	__say "${DOCKER_TEST_BUILD_OUTPUT}" >&2
	DOCKER_TEST_KEEP_ARTIFACTS=1
	__fail "The Makefile development build failed."
fi
__say "${DOCKER_TEST_BUILD_OUTPUT}"
DOCKER_TEST_BUILD_DIR="$(printf '%s\n' "${DOCKER_TEST_BUILD_OUTPUT}" | sed -n 's/^Development binaries: //p' | tail -n 1)"
if [[ -z "${DOCKER_TEST_BUILD_DIR}" || "${DOCKER_TEST_BUILD_DIR}" != "${DOCKER_TEST_BUILD_ROOT}/"* ]]; then
	DOCKER_TEST_KEEP_ARTIFACTS=1
	__fail "The Makefile did not report a build directory under the test artifact tree."
fi
if [[ ! -x "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}" || ! -x "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}-cli" ]]; then
	DOCKER_TEST_KEEP_ARTIFACTS=1
	__fail "The Makefile build did not produce both server and CLI binaries."
fi

__test_shipped_image

timeout 30s docker run --detach --init --name "${DOCKER_TEST_CONTAINER_NAME}" --network none \
	--memory=512m --cpus=1 --pids-limit=64 --security-opt=no-new-privileges:true \
	--tmpfs /tmp:rw,nosuid,nodev,noexec,size=64m --tmpfs /run:rw,nosuid,nodev,noexec,size=8m \
	--volume "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}:/app/${DOCKER_TEST_PROJECT_NAME}:ro" \
	--volume "${DOCKER_TEST_BUILD_DIR}/${DOCKER_TEST_PROJECT_NAME}-cli:/app/${DOCKER_TEST_PROJECT_NAME}-cli:ro" \
	--volume "${DOCKER_TEST_CONFIG_DIR}:/config:rw" \
	--volume "${DOCKER_TEST_DATA_DIR}:/data:rw" \
	--volume "${DOCKER_TEST_LOG_DIR}:/logs:rw" \
		--entrypoint sh "${DOCKER_TEST_IMAGE}" -c 'sleep 1800' >/dev/null

__say "Starting the server inside the isolated container..."
timeout 15s docker exec --detach "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" \
	--address 0.0.0.0 --port "${DOCKER_TEST_PORT}" --config /config --data /data --log /logs --color no
DOCKER_TEST_HEALTHY=0
DOCKER_TEST_DEADLINE=$((SECONDS + 30))
while (( SECONDS < DOCKER_TEST_DEADLINE )); do
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

__say "Checking server and CLI flags..."
timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --help >/dev/null
timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --version >/dev/null
timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --help >/dev/null
timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}-cli" --version >/dev/null
if ! timeout 15s docker exec "${DOCKER_TEST_CONTAINER_NAME}" "/app/${DOCKER_TEST_PROJECT_NAME}" --port "${DOCKER_TEST_PORT}" --config /config --data /data --log /logs --status >/dev/null; then
	__fail "The server --status check failed while the server was running."
fi
printf '%sPASS:%s server and CLI flags\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"

__say "Checking web pages, REST, GraphQL, discovery, and text endpoints..."
__assert_contains "home SSR page" "/" "Countries"
__assert_contains "search SSR page" "/search?q=Japan" "Japan"
__assert_contains "coordinate SSR page" "/coordinates?lat=40.7128&lon=-74.0060" "Bermuda"
__assert_contains "about server page" "/server/about" "Countries"
__assert_contains "DB-IP attribution on about page" "/server/about" '<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>'
__assert_contains "help server page" "/server/help" "Countries"
__assert_text_contains "health server page" "/server/healthz" "Status:"
__assert_contains "privacy server page" "/server/privacy" "Privacy"
__assert_contains "terms server page" "/server/terms" "Terms"
__assert_contains "security server page" "/server/security" "Security"
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
__assert_contains "about API mirror" "/api/v1/server/about" "Features"
__assert_contains "help API mirror" "/api/v1/server/help" "Find countries"
__assert_contains "privacy API mirror" "/api/v1/server/privacy" "data_stored_on_server"
__assert_contains "terms API mirror" "/api/v1/server/terms" "Terms"
__assert_contains "security API mirror" "/api/v1/server/security" "contacts"
__assert_contains "contact API mirror" "/api/v1/server/contact" "types"
__assert_contains "DPO API mirror" "/api/v1/server/dpo" "privacy_url"
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
__assert_text_contains "root health alias" "/api/healthz" "status:"
__assert_contains "Swagger API alias" "/api/swagger" "openapi"
__assert_contains "GraphQL countries GET query" "/api/v1/server/graphql?query=%7B%20countries%20%7B%20name%20%7B%20common%20%7D%20%7D%20%7D" "Japan"
__assert_post_contains "GraphQL countries API" "/api/v1/server/graphql" '{"query":"{ countries { name { common } } }"}' "Japan"
__assert_post_contains "GraphQL alias" "/api/graphql" '{"query":"{ countries { name { common } } }"}' "Japan"
__assert_post_contains "nearest country POST API" "/api/v1/coordinates" '{"lat":40.7128,"lon":-74.0060}' "Bermuda"

__assert_cli_contains "CLI list" "Afghanistan" list
__assert_cli_contains "CLI country lookup" "United States" get US
__assert_cli_contains "CLI search" "Japan" search Japan
__assert_cli_contains "CLI nearest-coordinate lookup" "Bermuda" nearest 40.7128 -74.0060
__assert_cli_contains "CLI nearby-country lookup" "United States" nearby 38 -97 250
__assert_cli_contains "CLI stats" "total_countries" stats

if timeout 10s docker exec "${DOCKER_TEST_CONTAINER_NAME}" wget -qO- "${DOCKER_TEST_BASE_URL}/not-a-real-page" >/dev/null 2>&1; then
	__fail "An unknown page unexpectedly returned a successful response."
fi
printf '%sPASS:%s unknown page returns an error\n' "${DOCKER_TEST_GREEN}" "${DOCKER_TEST_RESET}"

__say "Docker binary validation passed."

# ex: ts=4 sw=4 et filetype=sh
