#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202611241918-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME           :  README.md
# @@Copyright        :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created          :  Sunday, November 22, 2026 00:00 EST
# @@File              :  incus.sh
# @@Description       :  Validate countries service behavior in isolated Debian and Alpine Incus instances.
# @@Changelog         :  Use Incus-managed DNS for Alpine package setup and fail when test prerequisites cannot install.
# @@TODO              :  None
# @@Other             :  Requires Incus, Make, and GNU timeout.
# @@Resource          :  AI.md PARTS 0, 23, 24, 26, 28, and 31
# @@Terminal App      :  no
# @@sudo/root         :  no
# @@Template          :  shell/bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
# shellcheck disable=SC1001,SC1003,SC2001,SC2003,SC2016,SC2031,SC2090,SC2115,SC2120,SC2155,SC2199,SC2229,SC2317,SC2329
# - - - - - - - - - - - - - - - - - - - - - - - - -

set -eo pipefail

APPNAME="${0##*/}"
# shellcheck disable=SC2034
VERSION="202611241918-git"
# shellcheck disable=SC2034
RUN_USER="${USER:-unknown}"
# shellcheck disable=SC2034
SET_UID="${UID:-$(id -u)}"
SCRIPT_SRC_DIR="${BASH_SOURCE[0]%/*}"
INCUS_PROJECT_DIR="$(cd "${SCRIPT_SRC_DIR}/.." && pwd)"
INCUS_PROJECT_NAME="$(awk -F ': *' '$1 == "project_name" { print $2; exit }' "${INCUS_PROJECT_DIR}/IDEA.md")"
INCUS_PROJECT_ORG="$(awk -F ': *' '$1 == "project_org" { print $2; exit }' "${INCUS_PROJECT_DIR}/IDEA.md")"
INCUS_INTERNAL_NAME="$(awk -F ': *' '$1 == "internal_name" { print $2; exit }' "${INCUS_PROJECT_DIR}/IDEA.md")"
INCUS_INTERNAL_ORG="$(awk -F ': *' '$1 == "internal_org" { print $2; exit }' "${INCUS_PROJECT_DIR}/IDEA.md")"
INCUS_MODE="${1:-all}"
INCUS_TIMEOUT="${INCUS_TIMEOUT:-1800}"
INCUS_TEMP_DIR=""
INCUS_INSTANCES=()

__fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
__incus() { timeout "${INCUS_TIMEOUT}s" incus "$@"; }

__cleanup() {
	local status=$? instance
	for instance in "${INCUS_INSTANCES[@]}"; do
		if timeout 15s incus info "${instance}" >/dev/null 2>&1; then
			if ! timeout 30s incus delete "${instance}" --force >/dev/null 2>&1; then
				printf 'FAIL: unable to remove test instance %s\n' "${instance}" >&2
				status=1
			fi
		fi
	done
	if [[ -n "${INCUS_TEMP_DIR}" ]]; then
		if [[ "${status}" -eq 0 ]]; then
			if [[ "${INCUS_TEMP_DIR}" == "${TMPDIR:-/tmp}/${INCUS_PROJECT_ORG}/${INCUS_INTERNAL_NAME}-"* ]]; then
				rm -rf -- "${INCUS_TEMP_DIR}" || status=1
			else
				printf 'Refusing to remove unexpected artifact path %s\n' "${INCUS_TEMP_DIR}" >&2
				status=1
			fi
		else
			printf 'Test artifacts preserved at %s\n' "${INCUS_TEMP_DIR}" >&2
		fi
	fi
	exit "${status}"
}

__on_int() { exit 130; }
__on_term() { exit 143; }

__wait_boot() {
	local instance="$1" mode="$2" attempt state
	for ((attempt=0; attempt<60; attempt++)); do
		if __incus exec "${instance}" -- true >/dev/null 2>&1; then break; fi
		sleep 1
	done
	if [[ "${mode}" == systemd ]]; then
		for ((attempt=0; attempt<90; attempt++)); do
			state="$(__incus exec "${instance}" -- systemctl is-system-running 2>/dev/null || true)"
			[[ -n "${state}" && "${state}" != starting ]] && break
			sleep 1
		done
	else
		for ((attempt=0; attempt<60; attempt++)); do
			if __incus exec "${instance}" -- test -d /run/openrc >/dev/null 2>&1; then break; fi
			sleep 1
		done
	fi
}

__install_openrc_packages() {
	local instance="$1" attempt
	for attempt in 1 2 3; do
		if __incus exec "${instance}" -- sh -c 'apk add --no-cache bash curl jq file tor i2pd tzdata ca-certificates iproute2 shadow'; then
			return 0
		fi
		(( attempt == 3 )) || sleep $((attempt * 5))
	done
	return 1
}

__run_mode() {
	local mode="$1" image instance server_bin cli_bin
	if [[ "${mode}" == systemd ]]; then image="images:debian/trixie"; else image="images:alpine/3.24"; fi
	instance="${INCUS_PROJECT_NAME}-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
	if __incus info "${instance}" >/dev/null 2>&1; then __fail "Refusing to use existing instance ${instance}."; fi
	INCUS_INSTANCES+=("${instance}")
	printf '=== %s: launch %s ===\n' "${mode}" "${image}"
	__incus launch "${image}" "${instance}" -c limits.cpu=2 -c limits.memory=4GiB -c limits.cpu.priority=5
	__wait_boot "${instance}" "${mode}"
	if [[ "${mode}" == systemd ]]; then
		__incus exec "${instance}" -- bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq bash curl jq file tor i2pd iproute2 ca-certificates' >/dev/null
		__incus exec "${instance}" -- bash -c 'systemctl disable --now tor.service tor@default.service i2pd.service >/dev/null 2>&1 || true'
	else
		__install_openrc_packages "${instance}" >/dev/null || __fail "Unable to install Alpine integration-test packages."
		__incus exec "${instance}" -- sh -c 'rc-service tor stop >/dev/null 2>&1 || true; rc-service i2pd stop >/dev/null 2>&1 || true; rc-update del tor default >/dev/null 2>&1 || true; rc-update del i2pd default >/dev/null 2>&1 || true'
	fi
	server_bin="${INCUS_BUILD_DIR}/${INCUS_PROJECT_NAME}"
	cli_bin="${INCUS_BUILD_DIR}/${INCUS_PROJECT_NAME}-cli"
	__incus file push "${server_bin}" "${instance}/usr/local/bin/${INCUS_PROJECT_NAME}"
	__incus exec "${instance}" -- chmod 0755 "/usr/local/bin/${INCUS_PROJECT_NAME}"
	if [[ -x "${cli_bin}" ]]; then
		__incus file push "${cli_bin}" "${instance}/usr/local/bin/${INCUS_PROJECT_NAME}-cli"
		__incus exec "${instance}" -- chmod 0755 "/usr/local/bin/${INCUS_PROJECT_NAME}-cli"
	fi
	__run_remote "${instance}" "${mode}" "${INCUS_REMOTE_LIB}"$'\n'"${INCUS_REMOTE_MAIN}"
	printf '=== %s: restart container ===\n' "${mode}"
	__incus restart "${instance}"
	__wait_boot "${instance}" "${mode}"
	__run_remote "${instance}" "${mode}" "${INCUS_REMOTE_LIB}"$'\n'"${INCUS_REMOTE_AFTER}"
}

__run_remote() {
	local instance="$1" mode="$2" script="$3"
	printf '%s\n' "${script}" | __incus exec "${instance}" -- env \
		INIT_MODE="${mode}" PROJECT_NAME="${INCUS_PROJECT_NAME}" \
		INTERNAL_NAME="${INCUS_INTERNAL_NAME}" INTERNAL_ORG="${INCUS_INTERNAL_ORG}" \
		TOR_WAIT="${TOR_WAIT:-300}" I2P_WAIT="${I2P_WAIT:-600}" bash -s
}

trap '__cleanup' EXIT
trap '__on_int' INT
trap '__on_term' TERM

[[ "${INCUS_MODE}" =~ ^(all|systemd|openrc)$ ]] || __fail "Usage: ${APPNAME} [systemd|openrc|all]"
for value in "${INCUS_PROJECT_NAME}" "${INCUS_PROJECT_ORG}" "${INCUS_INTERNAL_NAME}" "${INCUS_INTERNAL_ORG}"; do
	[[ "${value}" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || __fail "Invalid project identity in IDEA.md."
done
command -v incus >/dev/null 2>&1 || __fail "Incus is required."
command -v make >/dev/null 2>&1 || __fail "Make is required."
command -v timeout >/dev/null 2>&1 || __fail "GNU timeout is required."
__incus info >/dev/null 2>&1 || __fail "Incus is unavailable or not initialized."

INCUS_TEMP_ROOT="${TMPDIR:-/tmp}/${INCUS_PROJECT_ORG}"
mkdir -p "${INCUS_TEMP_ROOT}"
INCUS_TEMP_DIR="$(mktemp -d "${INCUS_TEMP_ROOT}/${INCUS_INTERNAL_NAME}-XXXXXX")"
chmod 0700 "${INCUS_TEMP_DIR}"
mkdir -p "${INCUS_TEMP_DIR}/artifacts/build"
INCUS_BUILD_DIR="${INCUS_TEMP_DIR}/artifacts/build"
INCUS_RELEASE_DIR="${INCUS_TEMP_DIR}/artifacts/releases"
cd "${INCUS_PROJECT_DIR}"
printf '=== Building through Makefile ===\n'
if ! timeout "${INCUS_TIMEOUT}s" make build BINDIR="${INCUS_BUILD_DIR}" RELDIR="${INCUS_RELEASE_DIR}" >"${INCUS_TEMP_DIR}/artifacts/build.log" 2>&1; then
	__fail "make build failed; see ${INCUS_TEMP_DIR}/artifacts/build.log"
fi
[[ -x "${INCUS_BUILD_DIR}/${INCUS_PROJECT_NAME}" ]] || __fail "Expected server binary missing from Makefile build output."

INCUS_REMOTE_LIB=$(cat <<'REMOTE_LIB'
FAILURES=0
fail() { printf 'FAIL: %s\n' "$*"; FAILURES=$((FAILURES + 1)); }
pass() { printf 'PASS: %s\n' "$*"; }
check() { local label="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$label"; else fail "$label"; fi; }
check_not() { local label="$1"; shift; if "$@" >/dev/null 2>&1; then fail "$label"; else pass "$label"; fi; }
check_verbose() { local label="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$label"; else fail "$label"; "$@" >&2 || true; fi; }
wait_for() { local seconds="$1" i; shift; for ((i=0; i<seconds; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }
CONFIG_FILE="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}/server.yml"
DATA_ROOT="/var/lib/${INTERNAL_ORG}/${INTERNAL_NAME}"
CONF_ROOT="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}"
port() { sed -n '/^server:/,/^[^[:space:]]/s/^[[:space:]]*port:[[:space:]]*"\{0,1\}\([0-9]\{1,5\}\).*/\1/p' "$CONFIG_FILE" | head -1; }
api() { printf 'http://127.0.0.1:%s/api/v1/server/healthz' "$(port)"; }
healthz() { curl -q -LSsf --max-time 10 -H 'Accept: application/json' "$(api)"; }
healthz_up() { healthz | jq -e '.status' >/dev/null; }
check_health() {
	local label="$1" seconds="$2"
	if wait_for "$seconds" healthz_up; then
		pass "$label"
	else
		fail "$label"
		printf 'Health endpoint %s was unavailable; configured port is %s.\n' "$(api)" "$(port)" >&2
		curl -q -LSs --max-time 10 -i -H 'Accept: application/json' "$(api)" >&2 || true
		if [ "$INIT_MODE" = systemd ]; then
			systemctl status --no-pager -l "$INTERNAL_NAME" >&2 || true
			journalctl -u "$INTERNAL_NAME" --no-pager -n 80 >&2 || true
		else
			find "$DATA_ROOT" "$CONF_ROOT" -maxdepth 3 -type f -name '*.log' -exec tail -n 80 {} \; >&2 || true
		fi
	fi
}
overlay() { healthz | jq -r ".features.$1.$2"; }
if [ "$INIT_MODE" = systemd ]; then
 UNIT_FILE="/etc/systemd/system/${INTERNAL_NAME}.service"
 svc_active() { systemctl is-active --quiet "$INTERNAL_NAME"; }
 svc_enabled() { systemctl is-enabled --quiet "$INTERNAL_NAME"; }
 svc_pid() { systemctl show -p MainPID --value "$INTERNAL_NAME"; }
else
 UNIT_FILE="/etc/init.d/${INTERNAL_NAME}"
 svc_active() { rc-service "$INTERNAL_NAME" status 2>&1 | grep -q started; }
 svc_enabled() { rc-update show default | grep -qw "$INTERNAL_NAME"; }
 svc_pid() { cat "/var/run/${INTERNAL_ORG}/${INTERNAL_NAME}/${INTERNAL_NAME}.pid"; }
fi
svc_stopped() { ! svc_active; }
REMOTE_LIB
)

INCUS_REMOTE_MAIN=$(cat <<'REMOTE_MAIN'
echo "=== $INIT_MODE init identity ==="
if [ "$INIT_MODE" = systemd ]; then
 check "systemd is PID 1" test "$(cat /proc/1/comm)" = systemd
else
 check "OpenRC is active" test -d /run/openrc
 check "systemd is absent" test ! -d /run/systemd/system
fi
check "server version" "/usr/local/bin/${PROJECT_NAME}" --version
check "server help" "/usr/local/bin/${PROJECT_NAME}" --help
check "server binary metadata" file "/usr/local/bin/${PROJECT_NAME}"
"/usr/local/bin/${PROJECT_NAME}" --service --install
check "service file installed" test -f "$UNIT_FILE"
if [ "$INIT_MODE" = openrc ]; then
 check "OpenRC shebang" sh -c "head -1 '$UNIT_FILE' | grep -q '^#!/sbin/openrc-run'"
 check "OpenRC script executable" test -x "$UNIT_FILE"
 check_not "OpenRC is not SysV" grep -q 'BEGIN INIT INFO' "$UNIT_FILE"
fi
check "service enabled" svc_enabled
check "service active" wait_for 30 svc_active
check_health "health answers" 60
check "service account exists" getent passwd "$INTERNAL_NAME"
check "service dropped privileges" test "$(stat -c %U /proc/$(svc_pid))" = "$INTERNAL_NAME"
old_pid=$(svc_pid)
"/usr/local/bin/${PROJECT_NAME}" --service restart
check "service active after restart" wait_for 30 svc_active
check_health "health after restart" 60
check "restart replaced process" test "$(svc_pid)" != "$old_pid"
"/usr/local/bin/${PROJECT_NAME}" --service stop
check "service stopped" wait_for 30 svc_stopped
check_not "port closed while stopped" curl -q -LSsf --max-time 3 "http://127.0.0.1:$(port)/api/v1/server/healthz"
"/usr/local/bin/${PROJECT_NAME}" --service start
check "service starts" wait_for 30 svc_active
check_health "health after start" 60
base="http://127.0.0.1:$(port)"
for route in / /search?q=Japan /coordinates?lat=40.7128\&lon=-74.0060 /server/about /server/contact /server/dpo /server/help /server/healthz /server/preferences /server/privacy /server/security /server/terms /server/docs/swagger /server/docs/graphql /api/v1/countries/US /api/v1/countries/search?q=Japan '/api/v1/countries?region=Asia&limit=1' /api/v1/coordinates?lat=40.7128\&lon=-74.0060 '/api/v1/nearby?lat=38&lon=-97&radius=250' /api/v1/stats /api/v1/count /api/v1/server/swagger /.well-known/security.txt /llms.txt /robots.txt /manifest.json /sw.js /sitemap.xml /api/healthz /api/swagger; do
 body=$(curl -q -LSsf --max-time 15 "$base$route") || { fail "GET $route"; continue; }
 [ -n "$body" ] && pass "GET $route" || fail "empty GET $route"
done
check "stats text endpoint" sh -c "curl -q -LSsf '$base/api/v1/stats.txt' | grep -q 'Total Countries: 249'"
check "count text endpoint" sh -c "curl -q -LSsf '$base/api/v1/count.txt' | grep -q 249"
check "GraphQL countries query" sh -c "curl -q -LSsf -H 'Content-Type: application/json' -d '{\"query\":\"{ countries { name { common } } }\"}' '$base/api/v1/server/graphql' | grep -q Japan"
check "GraphQL countries GET query" sh -c "curl -q -LSsf '$base/api/v1/server/graphql?query=%7B%20countries%20%7B%20name%20%7B%20common%20%7D%20%7D%20%7D' | grep -q Japan"
check "GraphQL alias" sh -c "curl -q -LSsf -H 'Content-Type: application/json' -d '{\"query\":\"{ countries { name { common } } }\"}' '$base/api/graphql' | grep -q Japan"
check_verbose "nearest-coordinate POST" sh -c "curl -q -LSsf -H 'Accept: application/json' -H 'Content-Type: application/json' -d '{\"lat\":40.7128,\"lon\":-74.0060}' '$base/api/v1/coordinates' | grep -q Bermuda"
check "HTML negotiation" sh -c "curl -q -LSsf -H 'Accept: text/html' '$base/' | grep -qi '<html'"
check "plain-text negotiation" sh -c "curl -q -LSsf -H 'Accept: text/plain' '$base/api/v1/countries/US' | grep -q 'United States'"
if [ -x "/usr/local/bin/${PROJECT_NAME}-cli" ]; then
	 check_verbose "CLI version" "/usr/local/bin/${PROJECT_NAME}-cli" --version
	 check_verbose "CLI help" "/usr/local/bin/${PROJECT_NAME}-cli" --help
	 check_verbose "CLI list" sh -c "/usr/local/bin/${PROJECT_NAME}-cli --server '$base' --output json list | grep -q Afghanistan"
	 check_verbose "CLI country lookup" sh -c "/usr/local/bin/${PROJECT_NAME}-cli --server '$base' --output json get US | grep -q 'United States'"
	 check_verbose "CLI search" sh -c "/usr/local/bin/${PROJECT_NAME}-cli --server '$base' --output json search Japan | grep -q Japan"
fi
cp "/usr/local/bin/${PROJECT_NAME}" /var/tmp/incus-renamed-server
check "renamed binary name detection" sh -c '/var/tmp/incus-renamed-server --help 2>&1 | grep -q incus-renamed-server'
check "Tor binary installed" command -v tor
check "Tor automatically enabled" test "$(overlay tor enabled)" = true
for i in $(seq 1 "$TOR_WAIT"); do [ "$(overlay tor status)" = healthy ] && break; sleep 1; done
if [ "$(overlay tor status)" = healthy ]; then
 check "Tor v3 onion hostname" sh -c "echo '$(overlay tor hostname)' | grep -Eq '^[a-z2-7]{56}\\.onion$'"
 check "Tor running" test "$(overlay tor running)" = true
 check "Tor health check" test "$(healthz | jq -r '.checks.tor')" = ok
 check "Tor hostname persisted" test -s "$DATA_ROOT/tor/site/hostname"
else
 fail "Tor did not become healthy within ${TOR_WAIT}s"
fi
check "health response contains no filesystem path" sh -c "! healthz | grep -Eq '/(etc|var|home|config|data)/'"
check "I2P default is off outside Docker" test "$(overlay i2p enabled)" = false
mkdir -p "$CONF_ROOT/yaml.d"
printf 'server:\n  i2p:\n    enabled: true\n' > "$CONF_ROOT/yaml.d/50-i2p.yml"
chown "$INTERNAL_NAME:$INTERNAL_NAME" "$CONF_ROOT/yaml.d" "$CONF_ROOT/yaml.d/50-i2p.yml"
chmod 0700 "$CONF_ROOT/yaml.d"; chmod 0600 "$CONF_ROOT/yaml.d/50-i2p.yml"
"/usr/local/bin/${PROJECT_NAME}" --service restart
check_health "health after enabling I2P" 60
check "i2pd binary installed" command -v i2pd
check "I2P enabled by configuration" test "$(overlay i2p enabled)" = true
for i in $(seq 1 "$I2P_WAIT"); do [ "$(overlay i2p status)" = healthy ] && break; sleep 1; done
if [ "$(overlay i2p status)" = healthy ]; then
 check "I2P b32 hostname" sh -c "echo '$(overlay i2p hostname)' | grep -Eq '^[a-z2-7]{52}\\.b32\\.i2p$'"
 check "I2P running" test "$(overlay i2p running)" = true
 check "I2P health check" test "$(healthz | jq -r '.checks.i2p')" = ok
 check "i2pd has no forbidden listener ports" sh -c '! awk '\''$4 == "0A" { split($2, local, ":"); if (local[2] == "1B9E" || local[2] == "115C" || local[2] == "115F" || local[2] == "1DE6") found = 1 } END { exit found ? 0 : 1 }'\'' /proc/net/tcp /proc/net/tcp6'
 check "I2P key persisted" sh -c "find '$DATA_ROOT/i2p/site' -type f -size +0 | grep -q ."
else
 fail "I2P did not become healthy within ${I2P_WAIT}s"
fi
rm -f "$CONF_ROOT/yaml.d/50-i2p.yml"
"/usr/local/bin/${PROJECT_NAME}" --service restart
check_health "health after I2P opt-out" 60
check "I2P disabled after opt-out" test "$(overlay i2p enabled)" = false
check "I2P disabled status" test "$(overlay i2p status)" = disabled
[ "$FAILURES" -eq 0 ] || exit 1
REMOTE_MAIN
)

INCUS_REMOTE_AFTER=$(cat <<'REMOTE_AFTER'
echo "=== $INIT_MODE boot persistence ==="
check "service remains enabled" svc_enabled
check "service active after container restart" wait_for 120 svc_active
check "health after container restart" wait_for 120 healthz_up
"/usr/local/bin/${PROJECT_NAME}" --service --disable
check_not "service disabled at boot" svc_enabled
check "service stopped after disable" wait_for 30 svc_stopped
check "service file retained" test -f "$UNIT_FILE"
"/usr/local/bin/${PROJECT_NAME}" --service --install
check "service enabled after reinstall" svc_enabled
check "service active after reinstall" wait_for 30 svc_active
check "service uninstall succeeds" sh -c "printf 'y\\n' | /usr/local/bin/${PROJECT_NAME} --service --uninstall"
check_not "service file removed" test -e "$UNIT_FILE"
check_not "service no longer enabled" svc_enabled
check "service stopped after uninstall" wait_for 30 svc_stopped
check_not "service data removed" test -e "$DATA_ROOT"
check_not "service config removed" test -e "$CONF_ROOT"
check_not "service account removed" id -u "$INTERNAL_NAME"
check_not "service account marker removed" test -e "/etc/${INTERNAL_ORG}/.${INTERNAL_NAME}-service-account-created"
[ "$FAILURES" -eq 0 ] || exit 1
REMOTE_AFTER
)

case "${INCUS_MODE}" in
	all) __run_mode systemd; __run_mode openrc ;;
	systemd|openrc) __run_mode "${INCUS_MODE}" ;;
esac
printf 'Incus integration tests passed (%s).\n' "${INCUS_MODE}"

# ex: ts=4 sw=4 et filetype=sh
