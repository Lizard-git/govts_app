#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/sonoryx}"
LIVE_BIN="$APP_DIR/sonoryx-server"
NEW_BIN="${1:-$APP_DIR/sonoryx-server.upload}"
BACKUP_BIN="$APP_DIR/sonoryx-server.previous"
CONFIG="${CONFIG:-$APP_DIR/server.json}"
DB_PATH="${DB_PATH:-$APP_DIR/sonoryx.db}"
DEPLOY_READY_FILE=""
DEPLOY_ACTIVATE_FILE=""
LOG_FILE="${LOG_FILE:-$APP_DIR/server.log}"
PID_FILE="${PID_FILE:-$APP_DIR/server.pid}"
VOICE_PORT="${VOICE_PORT:-9000}"
MEDIA_PORT="${MEDIA_PORT:-9002}"
FILE_PORT="${FILE_PORT:-9001}"
MEDIA_MIN_PORT="${MEDIA_MIN_PORT:-20000}"
MEDIA_MAX_PORT="${MEDIA_MAX_PORT:-20100}"
: "${PUBLIC_IP:?set PUBLIC_IP to the server public IP}"
STOP_TIMEOUT="${STOP_TIMEOUT:-15}"
START_TIMEOUT="${START_TIMEOUT:-120}"
TMUX_SESSION="${TMUX_SESSION:-sonoryx-server}"
TMUX_PANE="=$TMUX_SESSION:0.0"
VOICE_REDUNDANCY="${VOICE_REDUNDANCY:-0}"
VOICE_UPLINK_REDUNDANCY="${VOICE_UPLINK_REDUNDANCY:-0}"
PUBLIC_STATUS="${PUBLIC_STATUS:-0}"
MEDIA_ADAPTIVE="${MEDIA_ADAPTIVE:-0}"

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
  [[ "$cwd" == "$APP_DIR" ]] || return 1
  [[ "$exe" == "$APP_DIR"/sonoryx-server* || "$exe" == "$APP_DIR"/govts-server* ]]
}

current_pid() {
  local pid="" candidates=""

  if tmux has-session -t "=$TMUX_SESSION" 2>/dev/null; then
    pid="$(tmux display-message -p -t "$TMUX_PANE" '#{pane_pid}' 2>/dev/null || true)"
    valid_server_pid "$pid" \
      || fail "tmux session $TMUX_SESSION exists but does not run the expected server; inspect it manually"
    printf '%s\n' "$pid"
    return 0
  fi

  if [[ -f "$PID_FILE" ]]; then
    pid="$(<"$PID_FILE")"
    if valid_server_pid "$pid"; then
      printf '%s\n' "$pid"
      return 0
    fi
    rm -f "$PID_FILE"
  fi

  # Compatibility with old sonoryx-server-N and pre-rename govts-server processes.
  candidates="$(listener_pid)"
  [[ "$(wc -w <<<"$candidates")" -le 1 ]] \
    || fail "multiple processes listen on UDP :$VOICE_PORT: $candidates"
  if [[ -n "$candidates" ]] && valid_server_pid "$candidates"; then
    printf '%s\n' "$candidates"
  fi
}

stop_server() {
  local pid="$1" deadline pane_pid=""
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
  deadline=$((SECONDS + 5))
  while kill -0 "$pid" 2>/dev/null; do
    (( SECONDS < deadline )) || fail "PID $pid has not exited; refusing database restore"
    sleep 1
  done
  if tmux has-session -t "=$TMUX_SESSION" 2>/dev/null; then
    pane_pid="$(tmux display-message -p -t "$TMUX_PANE" '#{pane_pid}' 2>/dev/null || true)"
    if [[ "$pane_pid" == "$pid" ]]; then
      tmux kill-session -t "=$TMUX_SESSION" 2>/dev/null || true
    fi
  fi
  rm -f "$PID_FILE"
}

binary_supports_flag() {
  # Go's flag package prints the usage, including every flag name, for -h.
  local usage
  usage="$("$LIVE_BIN" -h 2>&1 || true)"
  [[ "$usage" == *"-$1"* ]]
}

start_server() {
  local pid deadline effective_file_port=0 file_status="disabled"
  local -a extra_args=()
  extra_args+=(-db "$DB_PATH")
  if [[ -n "$DEPLOY_READY_FILE" ]]; then
    extra_args+=(-deploy-ready-file "$DEPLOY_READY_FILE" -deploy-activate-file "$DEPLOY_ACTIVATE_FILE")
  fi
  if tmux has-session -t "=$TMUX_SESSION" 2>/dev/null; then
    echo "tmux session $TMUX_SESSION already exists" >&2
    return 1
  fi
  if binary_supports_flag file-port; then
    extra_args+=(-file-port "$FILE_PORT")
    effective_file_port="$FILE_PORT"
    if [[ "$FILE_PORT" != "0" ]]; then
      file_status="TCP :$FILE_PORT"
      echo "File sharing: enabled on $file_status"
    else
      echo "File sharing: disabled"
    fi
  elif [[ "$FILE_PORT" != "0" ]]; then
    # A rollback binary may predate file sharing.
    echo "WARNING: $LIVE_BIN does not support -file-port; file sharing is disabled" >&2
  fi
  if [[ "$VOICE_REDUNDANCY" == "1" ]]; then
    # A rollback binary may predate the flag; start it without redundancy.
    if binary_supports_flag voice-redundancy; then
      extra_args+=(-voice-redundancy)
      echo "Voice redundancy: enabled"
    else
      echo "WARNING: $LIVE_BIN does not support -voice-redundancy; starting without it" >&2
    fi
  fi
  if [[ "$VOICE_UPLINK_REDUNDANCY" == "1" ]]; then
    if binary_supports_flag voice-uplink-redundancy; then
      extra_args+=(-voice-uplink-redundancy)
      echo "Uplink voice redundancy: enabled"
    else
      echo "WARNING: $LIVE_BIN does not support -voice-uplink-redundancy; starting without it" >&2
    fi
  fi
  if [[ "$PUBLIC_STATUS" == "1" ]]; then
    if binary_supports_flag public-status; then
      extra_args+=(-public-status)
      echo "Public server status: enabled"
    else
      echo "WARNING: $LIVE_BIN does not support -public-status; starting without it" >&2
    fi
  fi
  if [[ "$MEDIA_ADAPTIVE" == "1" ]]; then
    if binary_supports_flag media-adaptive-bitrate; then
      extra_args+=(-media-adaptive-bitrate)
      echo "Adaptive screen bitrate: enabled"
    else
      echo "WARNING: $LIVE_BIN does not support -media-adaptive-bitrate; starting without it" >&2
    fi
  fi
  : >>"$LOG_FILE"
  printf 'Starting server command:'
  printf ' %q' "$LIVE_BIN" \
    -config "$CONFIG" \
    -port "$VOICE_PORT" \
    -media-port "$MEDIA_PORT" \
    -media-min-port "$MEDIA_MIN_PORT" \
    -media-max-port "$MEDIA_MAX_PORT" \
    -media-advertised-ip "$PUBLIC_IP" \
    ${extra_args[@]+"${extra_args[@]}"}
  printf '\n'
  tmux new-session -d -s "$TMUX_SESSION" -c "$APP_DIR" \
    bash -c 'log_file="$1"; shift; exec > >(tee -a "$log_file") 2>&1; exec "$@"' \
    _ "$LOG_FILE" "$LIVE_BIN" \
    -config "$CONFIG" \
    -port "$VOICE_PORT" \
    -media-port "$MEDIA_PORT" \
    -media-min-port "$MEDIA_MIN_PORT" \
    -media-max-port "$MEDIA_MAX_PORT" \
    -media-advertised-ip "$PUBLIC_IP" \
    ${extra_args[@]+"${extra_args[@]}"}
  pid="$(tmux display-message -p -t "$TMUX_PANE" '#{pane_pid}' 2>/dev/null || true)"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "$pid" >"$PID_FILE"

  deadline=$((SECONDS + START_TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      return 1
    fi
    if valid_server_pid "$pid" \
      && ss -H -lun "sport = :$VOICE_PORT" | grep -q . \
      && ss -H -ltn "sport = :$MEDIA_PORT" | grep -q . \
      && { [[ "$effective_file_port" == "0" ]] || ss -H -ltn "sport = :$effective_file_port" | grep -q .; } \
      && { [[ -z "$DEPLOY_READY_FILE" ]] || { [[ -f "$DEPLOY_READY_FILE" ]] && [[ "$(<"$DEPLOY_READY_FILE")" == "$pid ready" ]]; }; }; then
      echo "Server started: PID $pid in tmux session $TMUX_SESSION, UDP :$VOICE_PORT, media TCP :$MEDIA_PORT, files $file_status"
      echo "Console: tmux attach-session -t $TMUX_SESSION (detach with Ctrl-b, then d)"
      return 0
    fi
    sleep 1
  done
  return 1
}

command -v ss >/dev/null || fail "ss is required"
command -v readlink >/dev/null || fail "readlink is required"
command -v tmux >/dev/null || fail "tmux is required"
command -v tee >/dev/null || fail "tee is required"
[[ "$TMUX_SESSION" =~ ^[A-Za-z0-9_-]+$ ]] \
  || fail "TMUX_SESSION must contain only letters, digits, underscores or hyphens"
is_positive_integer "$VOICE_PORT" && (( VOICE_PORT <= 65535 )) \
  || fail "VOICE_PORT must be in range 1..65535"
is_positive_integer "$MEDIA_PORT" && (( MEDIA_PORT <= 65535 )) \
  || fail "MEDIA_PORT must be in range 1..65535"
[[ "$FILE_PORT" == "0" ]] || { is_positive_integer "$FILE_PORT" && (( FILE_PORT <= 65535 )); } \
  || fail "FILE_PORT must be in range 0..65535"
[[ "$FILE_PORT" == "0" ]] || (( FILE_PORT != MEDIA_PORT )) \
  || fail "FILE_PORT and MEDIA_PORT must be different TCP ports"
is_positive_integer "$MEDIA_MIN_PORT" && (( MEDIA_MIN_PORT <= 65535 )) \
  || fail "MEDIA_MIN_PORT must be in range 1..65535"
is_positive_integer "$MEDIA_MAX_PORT" && (( MEDIA_MAX_PORT <= 65535 )) \
  || fail "MEDIA_MAX_PORT must be in range 1..65535"
(( MEDIA_MIN_PORT <= MEDIA_MAX_PORT )) \
  || fail "MEDIA_MIN_PORT must not exceed MEDIA_MAX_PORT"
(( VOICE_PORT != MEDIA_PORT )) \
  || fail "VOICE_PORT and MEDIA_PORT must be different"
[[ "$VOICE_REDUNDANCY" == "0" || "$VOICE_REDUNDANCY" == "1" ]] \
  || fail "VOICE_REDUNDANCY must be 0 or 1"
[[ "$VOICE_UPLINK_REDUNDANCY" == "0" || "$VOICE_UPLINK_REDUNDANCY" == "1" ]] \
  || fail "VOICE_UPLINK_REDUNDANCY must be 0 or 1"
[[ "$PUBLIC_STATUS" == "0" || "$PUBLIC_STATUS" == "1" ]] \
  || fail "PUBLIC_STATUS must be 0 or 1"
[[ "$MEDIA_ADAPTIVE" == "0" || "$MEDIA_ADAPTIVE" == "1" ]] \
  || fail "MEDIA_ADAPTIVE must be 0 or 1"
is_positive_integer "$STOP_TIMEOUT" || fail "STOP_TIMEOUT must be positive"
is_positive_integer "$START_TIMEOUT" || fail "START_TIMEOUT must be positive"
[[ -d "$APP_DIR" ]] || fail "directory not found: $APP_DIR"
cd "$APP_DIR"
[[ -f "$CONFIG" ]] || fail "config not found: $CONFIG"
[[ -s "$NEW_BIN" ]] || fail "new binary not found or empty: $NEW_BIN"
[[ "$NEW_BIN" != "$LIVE_BIN" ]] || fail "pass a new file, not the live binary"
[[ "$DB_PATH" == /* ]] || DB_PATH="$APP_DIR/$DB_PATH"
[[ ! -L "$DB_PATH" ]] || fail "DB_PATH must name the database directly, not a symlink"
chmod 0755 "$NEW_BIN"
new_usage="$("$NEW_BIN" -h 2>&1 || true)"
[[ "$new_usage" == *"-deploy-ready-file"* ]] || fail "new binary must support staged deploy readiness"
if [[ "$FILE_PORT" != "0" ]]; then
  chmod 0755 "$NEW_BIN"
  new_usage="$("$NEW_BIN" -h 2>&1 || true)"
  [[ "$new_usage" == *"-file-port"* ]] \
    || fail "new binary does not support -file-port; build the updated server or set FILE_PORT=0"
fi

echo "WARNING: this script does not configure the firewall."
echo "Ensure inbound UDP :$VOICE_PORT, TCP :$MEDIA_PORT and UDP $MEDIA_MIN_PORT-$MEDIA_MAX_PORT are allowed."
if [[ "$FILE_PORT" != "0" ]]; then
  echo "Ensure inbound TCP :$FILE_PORT for file sharing is allowed."
fi
echo "Server config: $CONFIG"

old_pid="$(current_pid)"
old_exe=""
if [[ -n "$old_pid" ]]; then
  old_exe="$(readlink -f "/proc/$old_pid/exe" 2>/dev/null || true)"
fi

# Keep the staged binary on the same filesystem for atomic replacement.
install -m 0755 "$NEW_BIN" "$APP_DIR/sonoryx-server.next"
rm -f -- "$NEW_BIN"
ROLLBACK_DIR="$APP_DIR/.deploy-rollback"
install -d -m 0700 "$ROLLBACK_DIR"
if [[ ! -f "$LIVE_BIN" && -n "$old_exe" && -f "$old_exe" ]]; then
  install -m 0755 "$old_exe" "$LIVE_BIN"
fi
stop_server "$old_pid"
SNAPSHOT="$ROLLBACK_DIR/server.db"
snapshot_ready=0
had_database=0
live_replaced=0
started_new=0
activated=0
completed=0

rollback_on_exit() {
  local code="$?" failed_pid="" pane_pid=""
  trap - EXIT
  if (( code == 0 || completed == 1 )); then return; fi
  if (( activated == 1 )); then
    echo "Activation was issued; automatic database rollback is forbidden because new data may exist." >&2
    echo "Keep the current DB. Inspect $LOG_FILE. Previous binary: $BACKUP_BIN; pre-deploy DB: $SNAPSHOT" >&2
    exit "$code"
  fi
  echo "Deploy failed before activation; restoring the previous binary and database" >&2
  if (( started_new == 1 )); then
    [[ ! -f "$PID_FILE" ]] || failed_pid="$(<"$PID_FILE")"
    if tmux has-session -t "=$TMUX_SESSION" 2>/dev/null; then
      pane_pid="$(tmux display-message -p -t "$TMUX_PANE" '#{pane_pid}' 2>/dev/null || true)"
      if [[ -z "$failed_pid" ]] && valid_server_pid "$pane_pid"; then failed_pid="$pane_pid"; fi
      [[ -n "$failed_pid" && "$pane_pid" == "$failed_pid" ]] \
        || fail "cannot identify the new server; refusing database restore"
    fi
    if [[ -n "$failed_pid" ]] && kill -0 "$failed_pid" 2>/dev/null; then
      valid_server_pid "$failed_pid" || fail "unexpected PID; refusing database restore"
      stop_server "$failed_pid"
    elif tmux has-session -t "=$TMUX_SESSION" 2>/dev/null; then
      tmux kill-session -t "=$TMUX_SESSION"
    fi
    rm -f -- "$PID_FILE"
  fi
  if (( live_replaced == 1 )); then
    [[ -f "$BACKUP_BIN" ]] || fail "previous binary missing; restore manually from $ROLLBACK_DIR"
    install -m 0755 "$BACKUP_BIN" "$APP_DIR/sonoryx-server.restore"
    mv -f -- "$APP_DIR/sonoryx-server.restore" "$LIVE_BIN"
  fi
  if (( snapshot_ready == 1 )); then
    # All new processes have exited. Do not combine one version's WAL with another DB.
    rm -f -- "${DB_PATH}-wal" "${DB_PATH}-shm"
    install -m 0600 "$SNAPSHOT" "${DB_PATH}.restore"
    mv -f -- "${DB_PATH}.restore" "$DB_PATH"
  elif (( had_database == 0 && started_new == 1 )); then
    rm -f -- "$DB_PATH" "${DB_PATH}-wal" "${DB_PATH}-shm"
  fi
  DEPLOY_READY_FILE=""
  DEPLOY_ACTIVATE_FILE=""
  if [[ -f "$LIVE_BIN" ]]; then
    start_server || fail "previous version could not start; inspect $LOG_FILE and $ROLLBACK_DIR"
  fi
  exit "$code"
}
trap rollback_on_exit EXIT

rm -f -- "$ROLLBACK_DIR/ready" "$ROLLBACK_DIR/activate" "$ROLLBACK_DIR/snapshot.partial" \
  "$ROLLBACK_DIR/migration-check.db" "$ROLLBACK_DIR/migration-check.db-wal" "$ROLLBACK_DIR/migration-check.db-shm"
if [[ -f "$DB_PATH" ]]; then
  had_database=1
  # This command opens the SOURCE read-only and never runs its migrations.
  "$APP_DIR/sonoryx-server.next" --snapshot-db "$DB_PATH" "$ROLLBACK_DIR/snapshot.partial"
  mv -f -- "$ROLLBACK_DIR/snapshot.partial" "$SNAPSHOT"
  snapshot_ready=1
  install -m 0600 "$SNAPSHOT" "$ROLLBACK_DIR/migration-check.db"
  "$APP_DIR/sonoryx-server.next" --check-db "$ROLLBACK_DIR/migration-check.db"
  rm -f -- "$ROLLBACK_DIR/migration-check.db" "$ROLLBACK_DIR/migration-check.db-wal" "$ROLLBACK_DIR/migration-check.db-shm"
fi

if [[ -f "$LIVE_BIN" ]]; then
  install -m 0755 "$LIVE_BIN" "$BACKUP_BIN"
else
  fail "previous binary is required for managed redeploy"
fi
# Mark replacement before mv so EXIT recovery is safe even if interrupted here.
live_replaced=1
mv -f -- "$APP_DIR/sonoryx-server.next" "$LIVE_BIN"
DEPLOY_READY_FILE="$ROLLBACK_DIR/ready"
DEPLOY_ACTIVATE_FILE="$ROLLBACK_DIR/activate"
started_new=1
start_server || fail "new server did not become ready"
pid="$(<"$PID_FILE")"

# Set this BEFORE activation: failures after this line must preserve the working DB.
activated=1
printf '%s\n' "$pid" >"$ROLLBACK_DIR/activate.partial"
chmod 0600 "$ROLLBACK_DIR/activate.partial"
mv -f -- "$ROLLBACK_DIR/activate.partial" "$DEPLOY_ACTIVATE_FILE"
deadline=$((SECONDS + START_TIMEOUT))
while (( SECONDS < deadline )); do
  kill -0 "$pid" 2>/dev/null || fail "new server exited after activation; database was preserved"
  if [[ -f "$DEPLOY_READY_FILE" && "$(<"$DEPLOY_READY_FILE")" == "$pid active" ]]; then
    completed=1
    echo "Deploy activated. Previous binary and DB retained: $BACKUP_BIN, $SNAPSHOT"
    exit 0
  fi
  sleep 1
 done
fail "activation acknowledgement timed out; database was preserved"
