#!/bin/sh
set -eu

: "${CONFIG_DIR:=/config}"
: "${DATA_DIR:=/data}"
: "${LOGS_DIR:=/logs}"
: "${PORT:=80}"
export CONFIG_DIR DATA_DIR LOGS_DIR PORT

mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOGS_DIR"
exec /usr/local/bin/countries --port "$PORT" "$@"
