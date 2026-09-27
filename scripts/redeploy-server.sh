#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/govts}"
LIVE_BIN="$APP_DIR/govts-server"
NEW_BIN="${1:-$APP_DIR/govts-server.upload}"
BACKUP_BIN="$APP_DIR/govts-server.previous"
CONFIG="${CONFIG:-$APP_DIR/server.json}"
LOG_FILE="${LOG_FILE:-$APP_DIR/server.log}"
PID_FILE="${PID_FILE:-$APP_DIR/server.pid}"
VOICE_PORT="${VOICE_PORT:-9000}"
MEDIA_PORT="${MEDIA_PORT:-9002}"
MEDIA_MIN_PORT="${MEDIA_MIN_PORT:-20000}"
MEDIA_MAX_PORT="${MEDIA_MAX_PORT:-20100}"
PUBLIC_IP="${PUBLIC_IP:-193.187.92.89}"
STOP_TIMEOUT="${STOP_TIMEOUT:-15}"
START_TIMEOUT="${START_TIMEOUT:-10}"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

is_positive_integer() {
  [[ "$1" =~ ^[1-9][0-9]*$ ]]
}

listener_pid() {
  ss -H -lunp "sport = :$VOICE_PORT" 2>/dev/null \
    | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
    | sort -u
}

valid_server_pid() {
  local pid="$1" exe cwd
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
  cwd="$(readlink -f "/proc/$pid/cwd" 2>/dev/null || true)"
  [[ "$exe" == "$APP_DIR"/govts-server* && "$cwd" == "$APP_DIR" ]]
}

current_pid() {
  local pid="" candidates=""

  if [[ -f "$PID_FILE" ]]; then
    pid="$(<"$PID_FILE")"
    if valid_server_pid "$pid"; then
      printf '%s\n' "$pid"
      return 0
    fi
    rm -f "$PID_FILE"
  fi

  # Compatibility with the old manually started govts-server-N process.
  candidates="$(listener_pid)"
  [[ "$(wc -w <<<"$candidates")" -le 1 ]] \
    || fail "multiple processes listen on UDP :$VOICE_PORT: $candidates"
  if [[ -n "$candidates" ]] && valid_server_pid "$candidates"; then
    printf '%s\n' "$candidates"
  fi
}

stop_server() {
  local pid="$1" deadline
  [[ -n "$pid" ]] || return 0

  echo "Stopping PID $pid with SIGTERM"
  kill -TERM "$pid"
  deadline=$((SECONDS + STOP_TIMEOUT))
  while kill -0 "$pid" 2>/dev/null; do
    if (( SECONDS >= deadline )); then
      echo "Stop timeout; sending SIGKILL to PID $pid" >&2
      kill -KILL "$pid"
      break
    fi
    sleep 1
  done
  rm -f "$PID_FILE"
}

start_server() {
  nohup "$LIVE_BIN" \
    -config "$CONFIG" \
    -port "$VOICE_PORT" \
    -media-port "$MEDIA_PORT" \
    -media-min-port "$MEDIA_MIN_PORT" \
    -media-max-port "$MEDIA_MAX_PORT" \
    -media-advertised-ip "$PUBLIC_IP" \
    >>"$LOG_FILE" 2>&1 </dev/null &
  local pid=$!
  printf '%s\n' "$pid" >"$PID_FILE"

  local deadline=$((SECONDS + START_TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      return 1
    fi
    if ss -H -lun "sport = :$VOICE_PORT" | grep -q . \
      && ss -H -ltn "sport = :$MEDIA_PORT" | grep -q .; then
      echo "Server started: PID $pid, UDP :$VOICE_PORT, TCP :$MEDIA_PORT"
      return 0
    fi
    sleep 1
  done
  return 1
}

command -v ss >/dev/null || fail "ss is required"
command -v readlink >/dev/null || fail "readlink is required"
is_positive_integer "$VOICE_PORT" && (( VOICE_PORT <= 65535 )) \
  || fail "VOICE_PORT must be in range 1..65535"
is_positive_integer "$MEDIA_PORT" && (( MEDIA_PORT <= 65535 )) \
  || fail "MEDIA_PORT must be in range 1..65535"
is_positive_integer "$MEDIA_MIN_PORT" && (( MEDIA_MIN_PORT <= 65535 )) \
  || fail "MEDIA_MIN_PORT must be in range 1..65535"
is_positive_integer "$MEDIA_MAX_PORT" && (( MEDIA_MAX_PORT <= 65535 )) \
  || fail "MEDIA_MAX_PORT must be in range 1..65535"
(( MEDIA_MIN_PORT <= MEDIA_MAX_PORT )) \
  || fail "MEDIA_MIN_PORT must not exceed MEDIA_MAX_PORT"
(( VOICE_PORT != MEDIA_PORT )) \
  || fail "VOICE_PORT and MEDIA_PORT must be different"
is_positive_integer "$STOP_TIMEOUT" || fail "STOP_TIMEOUT must be positive"
is_positive_integer "$START_TIMEOUT" || fail "START_TIMEOUT must be positive"
[[ -d "$APP_DIR" ]] || fail "directory not found: $APP_DIR"
cd "$APP_DIR"
[[ -f "$CONFIG" ]] || fail "config not found: $CONFIG"
[[ -s "$NEW_BIN" ]] || fail "new binary not found or empty: $NEW_BIN"
[[ "$NEW_BIN" != "$LIVE_BIN" ]] || fail "pass a new file, not the live binary"

echo "WARNING: this script does not configure the firewall."
echo "Ensure inbound UDP :$VOICE_PORT, TCP :$MEDIA_PORT and UDP $MEDIA_MIN_PORT-$MEDIA_MAX_PORT are allowed."
echo "Server config: $CONFIG"

old_pid="$(current_pid)"
old_exe=""
if [[ -n "$old_pid" ]]; then
  old_exe="$(readlink -f "/proc/$old_pid/exe" 2>/dev/null || true)"
fi

# Keep the staged file and live file on the same filesystem for an atomic mv.
install -m 0755 "$NEW_BIN" "$APP_DIR/govts-server.next"
rm -f "$NEW_BIN"
stop_server "$old_pid"

rm -f "$BACKUP_BIN"
if [[ -f "$LIVE_BIN" ]]; then
  mv "$LIVE_BIN" "$BACKUP_BIN"
elif [[ -n "$old_exe" && -f "$old_exe" ]]; then
  # First managed deploy: retain the legacy govts-server-N binary.
  install -m 0755 "$old_exe" "$BACKUP_BIN"
fi
mv "$APP_DIR/govts-server.next" "$LIVE_BIN"

if start_server; then
  exit 0
fi

echo "New server failed to start; rolling back" >&2
failed_pid=""
if [[ -f "$PID_FILE" ]]; then
  failed_pid="$(<"$PID_FILE")"
fi
if [[ -n "$failed_pid" ]] && kill -0 "$failed_pid" 2>/dev/null; then
  stop_server "$failed_pid"
else
  rm -f "$PID_FILE"
fi

if [[ -f "$BACKUP_BIN" ]]; then
  mv -f "$BACKUP_BIN" "$LIVE_BIN"
  if start_server; then
    fail "new version failed; previous version was restored"
  fi
fi

fail "neither new nor previous version started; inspect $LOG_FILE"
