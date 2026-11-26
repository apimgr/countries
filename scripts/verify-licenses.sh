#!/usr/bin/env bash

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_DIR}"

PROJECT_ORG="$(awk -F ': *' '$1 == "project_org" { print $2; exit }' IDEA.md)"
INTERNAL_NAME="$(awk -F ': *' '$1 == "internal_name" { print $2; exit }' IDEA.md)"
if [[ -z "${PROJECT_ORG}" || -z "${INTERNAL_NAME}" ]]; then
	echo "ERROR: IDEA.md must define project_org and internal_name." >&2
	exit 1
fi

echo "Checking for incompatible licenses..."

command -v go-licenses >/dev/null 2>&1 || {
	echo "ERROR: go-licenses not found; run inside casjaysdev/go:latest" >&2
	exit 69
}

LICENSE_TEMP_ROOT="${TMPDIR:-/tmp}/${PROJECT_ORG}"
mkdir -p "${LICENSE_TEMP_ROOT}"
LICENSE_TEMP_DIR="$(mktemp -d "${LICENSE_TEMP_ROOT}/${INTERNAL_NAME}-XXXXXX")"
chmod 0700 "${LICENSE_TEMP_DIR}"
trap 'rm -rf -- "${LICENSE_TEMP_DIR}"' EXIT

declare -a IGNORE_ARGS=()
OVERRIDES_CSV="${LICENSE_TEMP_DIR}/license-overrides.csv"
: >"${OVERRIDES_CSV}"
OVERRIDES_FILE="${PROJECT_DIR}/.github/license-overrides.txt"
if [[ -f "${OVERRIDES_FILE}" ]]; then
	declare -A SEEN_OVERRIDES=()
	while IFS=$' \t' read -r module spdx reviewer_note; do
		[[ -z "${module}" || "${module}" == \#* ]] && continue
		if [[ -z "${spdx}" || -z "${reviewer_note}" || ! "${spdx}" =~ ^[A-Za-z0-9.+-]+$ ]]; then
			echo "ERROR: Invalid override entry in ${OVERRIDES_FILE}; expected module, SPDX identifier, and review note." >&2
			exit 1
		fi
		if [[ -n "${SEEN_OVERRIDES[${module}]:-}" ]]; then
			echo "ERROR: Duplicate license override for ${module}." >&2
			exit 1
		fi
		SEEN_OVERRIDES["${module}"]=1
		IGNORE_ARGS+=(--ignore "${module}")
		printf '%s,%s\n' "${module}" "${spdx}" >>"${OVERRIDES_CSV}"
	done <"${OVERRIDES_FILE}"
fi

echo "Scanning shipped Go packages under src/..."
# Capture the scanner result first so scanner errors cannot become an empty, passing report.
LICENSES="$(go-licenses csv ./src/... "${IGNORE_ARGS[@]}")"
LICENSES+=$'\n'
LICENSES+="$(cat "${OVERRIDES_CSV}")"
if printf '%s\n' "${LICENSES}" | grep -iE 'GPL|AGPL|LGPL'; then
	echo "ERROR: Copyleft license detected!" >&2
	echo "Remove the dependency or use an approved alternative." >&2
	exit 1
fi

echo "All detected licenses are compatible."
echo "Generating a temporary license report..."
printf '%s\n' "${LICENSES}" >"${LICENSE_TEMP_DIR}/licenses.csv"
go-licenses save ./src/... "${IGNORE_ARGS[@]}" --save_path="${LICENSE_TEMP_DIR}/third_party_licenses"

echo "License report and notices were generated in ${LICENSE_TEMP_DIR}; tracked inventories were not changed."
cat "${LICENSE_TEMP_DIR}/licenses.csv"
