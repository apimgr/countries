#!/usr/bin/env bash
# shellcheck shell=bash
# - - - - - - - - - - - - - - - - - - - - - - - - -
##@Version           :  202611231200-git
# @@Author           :  Jason Hempstead
# @@Contact          :  git-admin@casjaysdev.pro
# @@License          :  WTFPL
# @@ReadME            :  README.md
# @@Copyright         :  Copyright: (c) 2026 Jason Hempstead, Casjays Developments
# @@Created           :  Monday, November 23, 2026 12:00 EST
# @@File              :  run_tests.sh
# @@Description       :  Run the basic Docker binary integration test suite.
# @@Changelog         :  Make the basic integration runner Docker-only as specified by AI.md.
# @@TODO              :  None
# @@Other             :  Run the full OS suite separately with tests/incus.sh.
# @@Resource          :  AI.md PART 28
# @@Terminal App      :  no
# @@sudo/root         :  no
# @@Template          :  shell/bash
# - - - - - - - - - - - - - - - - - - - - - - - - -

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  printf 'ERROR: Docker is required because the binary integration suite builds with the project Go container.\n' >&2
  exit 69
fi

printf 'Docker detected; running the basic binary integration suite.\n'
exec "${SCRIPT_DIR}/docker.sh"
