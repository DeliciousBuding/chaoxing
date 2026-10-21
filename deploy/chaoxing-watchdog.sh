#!/bin/sh
# Recover the service after procd's bounded respawn (5 crashes/hour) gives up.
# Installed on the router as /usr/local/sbin/chaoxing-watchdog and invoked from
# cron through cudy-run, which provides locking, a timeout and failure alerts.
# It never touches a live process and rate-limits its own start attempts.
set -u

if pidof chaoxing >/dev/null 2>&1; then
    exit 0
fi

STATE=/mnt/sda1/services/chaoxing/.watchdog-last-start
NOW=$(date +%s)
LAST=0
if [ -f "$STATE" ]; then
    LAST=$(cat "$STATE" 2>/dev/null || echo 0)
fi
case "$LAST" in
    ''|*[!0-9]*) LAST=0 ;;
esac

if [ $((NOW - LAST)) -lt 900 ]; then
    echo "recent start attempt $((NOW - LAST))s ago; skipping"
    exit 0
fi

echo "$NOW" > "$STATE" 2>/dev/null || true
logger -t chaoxing-watchdog "process missing; starting service"
/etc/init.d/chaoxing start
sleep 6
if pidof chaoxing >/dev/null 2>&1; then
    logger -t chaoxing-watchdog "restart ok"
    exit 0
fi
logger -t chaoxing-watchdog "restart failed"
exit 1
