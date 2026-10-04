#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-large}"
DURATION_SECONDS="${2:-10}"
RPS="${3:-10}"
GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080/api/allfabs}"
MOCK_URL="${MOCK_URL:-http://localhost:8081}"

if [[ ! "$DURATION_SECONDS" =~ ^[1-9][0-9]*$ || ! "$RPS" =~ ^[1-9][0-9]*$ ]]; then
  printf 'DURATION_SECONDS and RPS must be positive integers\n' >&2
  exit 1
fi

case "$MODE" in
  large)
    CONFIG_URL="$MOCK_URL/admin/fabs/all?mode=success&page_items=3&payload_kb=1024"
    EXTRA_CONFIG_URL=""
    ;;
  hang)
    CONFIG_URL="$MOCK_URL/admin/fabs/all?mode=hang"
    EXTRA_CONFIG_URL=""
    ;;
  large-timeout)
    CONFIG_URL="$MOCK_URL/admin/fabs/all?mode=success&page_items=3&payload_kb=1024"
    EXTRA_CONFIG_URL="$MOCK_URL/admin/fabs/FAB_B?mode=timeout&delay_ms=20000"
    ;;
  all-timeout)
    CONFIG_URL="$MOCK_URL/admin/fabs/all?mode=timeout&delay_ms=20000"
    EXTRA_CONFIG_URL=""
    ;;
  all-error)
    CONFIG_URL="$MOCK_URL/admin/fabs/all?mode=error"
    EXTRA_CONFIG_URL=""
    ;;
  *)
    printf 'Usage: bash scripts/load-test.sh [large|hang|large-timeout|all-timeout|all-error] [duration_seconds] [rps]\n' >&2
    exit 1
    ;;
esac

reset_mock() {
  curl -fsS -X POST "$MOCK_URL/admin/reset" >/dev/null || true
}

trap reset_mock EXIT
trap 'exit 130' INT TERM

printf 'Resetting Mock API...\n'
reset_mock
printf 'Configuring %s scenario...\n' "$MODE"
curl -fsS -X PUT "$CONFIG_URL"
if [[ -n "$EXTRA_CONFIG_URL" ]]; then
  curl -fsS -X PUT "$EXTRA_CONFIG_URL"
fi
printf '\nStarting %d RPS for %ss (%d requests) against %s\n' \
  "$RPS" "$DURATION_SECONDS" "$((RPS * DURATION_SECONDS))" "$GATEWAY_URL"

started="$(date +%s)"
request=0
for ((second = 1; second <= DURATION_SECONDS; second++)); do
  for ((slot = 1; slot <= RPS; slot++)); do
    request="$((request + 1))"
    (
      result="$(curl -sS -o /dev/null -w 'status=%{http_code} total=%{time_total}s' "$GATEWAY_URL" || true)"
      printf 'request=%s %s\n' "$request" "$result"
    ) &
  done

  if [[ ( "$MODE" == "hang" || "$MODE" == "large-timeout" || "$MODE" == "all-timeout" ) && "$second" -eq 1 ]]; then
    sleep 1
    printf '\nMock state after the first second at %d RPS:\n' "$RPS"
    curl -fsS "$MOCK_URL/admin/state"
    printf '\n'
  elif [[ "$second" -lt "$DURATION_SECONDS" ]]; then
    sleep 1
  fi
done

wait
elapsed="$(( $(date +%s) - started ))"

printf '\nCompleted in approximately %ss\n' "$elapsed"
printf 'Final Mock state:\n'
curl -fsS "$MOCK_URL/admin/state"
printf '\nMock Prometheus metrics:\n'
curl -fsS "$MOCK_URL/metrics"
