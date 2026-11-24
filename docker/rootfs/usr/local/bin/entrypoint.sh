#!/bin/sh
set -eu

: "${APP_NAME:=countries}"
: "${CONFIG_DIR:=/config/${APP_NAME}}"
: "${DATA_DIR:=/data/${APP_NAME}}"
: "${CACHE_DIR:=/data/${APP_NAME}/cache}"
: "${LOG_DIR:=/data/log/${APP_NAME}}"
: "${DATABASE_DIR:=/data/db/sqlite}"
: "${BACKUP_DIR:=/data/backups/${APP_NAME}}"
: "${PORT:=80}"
export APP_NAME CONFIG_DIR DATA_DIR CACHE_DIR LOG_DIR DATABASE_DIR BACKUP_DIR PORT

exec /usr/local/bin/countries --port "$PORT" "$@"
