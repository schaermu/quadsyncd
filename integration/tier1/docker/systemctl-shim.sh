#!/bin/sh
# systemctl shim for Tier 1 integration tests
# Records all invocations and supports configurable exit codes

LOG_FILE="${SYSTEMCTL_LOG_FILE:-/tmp/systemctl.log}"

# Log the invocation with timestamp
echo "$(date -Iseconds) $*" >> "$LOG_FILE"

# Support exit code overrides for specific commands
case "$*" in
  *daemon-reload*)
    exit ${SYSTEMCTL_DAEMON_RELOAD_EXIT:-0}
    ;;
  *try-restart*)
    exit ${SYSTEMCTL_TRY_RESTART_EXIT:-0}
    ;;
  *is-system-running*)
    echo "running"
    exit ${SYSTEMCTL_IS_SYSTEM_RUNNING_EXIT:-0}
    ;;
  *status*)
    exit ${SYSTEMCTL_STATUS_EXIT:-0}
    ;;
  *)
    exit 0
    ;;
esac
