#!/usr/bin/env bash
# Refresh development binaries and enabled Windows autostart copies.
# Restart only servers that were running before `make install`.
set -euo pipefail
cd "$(dirname "$0")/.."

BIN=./bin/garminctl
LOG=bin/serve.log
URL=http://127.0.0.1:8787/
SYS32=/mnt/c/Windows/System32
PS="$SYS32/WindowsPowerShell/v1.0/powershell.exe"

# The pattern is anchored on purpose: an unanchored `pgrep -f garminctl serve`
# also matches the shell running this script.
wsl_pids() { pgrep -f '^\./bin/garminctl serve' || true; }

stop_wsl() {
	local pids
	pids=$(wsl_pids)
	[ -n "$pids" ] || return 0
	# shellcheck disable=SC2086 # pids is a whitespace-separated list
	kill $pids
	for _ in $(seq 50); do
		[ -n "$(wsl_pids)" ] || return 0
		sleep 0.2
	done
	echo "install: the dev server did not stop" >&2
	return 1
}

start_wsl() {
	nohup "$BIN" serve >>"$LOG" 2>&1 </dev/null &
	echo "dev server started (pid $!), log in $LOG"
}

have_windows() { [ -x "$SYS32/tasklist.exe" ] && [ -x "$PS" ]; }

windows_serving() {
	have_windows || return 1
	"$SYS32/tasklist.exe" /FI "IMAGENAME eq garminctl.exe" /NH 2>/dev/null | grep -q garminctl.exe
}

stop_windows() { "$SYS32/taskkill.exe" /IM garminctl.exe /F >/dev/null; }

start_windows() {
	"$PS" -NoProfile -NonInteractive -Command \
		'Start-Process -WindowStyle Hidden -FilePath (Join-Path $env:LOCALAPPDATA "garminctl\garminctl.exe") -ArgumentList serve'
	echo "Windows server started from %LOCALAPPDATA%\\garminctl"
}

# $1 selects a host-local probe, independent of WSL networking mode.
wait_ready() {
	local probe
	case "$1" in
	wsl) probe=(curl -fsS -o /dev/null "$URL") ;;
	windows) probe=("$SYS32/curl.exe" -fsS -o NUL "$URL") ;;
	esac
	for _ in $(seq 50); do
		if "${probe[@]}" 2>/dev/null; then
			echo "web UI answers at $URL ($1 server)"
			return 0
		fi
		sleep 0.2
	done
	echo "install: $URL did not answer within 10s ($1 server)" >&2
	return 1
}

was_wsl=0
[ -z "$(wsl_pids)" ] || was_wsl=1
[ "$was_wsl" = 0 ] || stop_wsl

if have_windows; then
	case "$("$BIN" autostart status 2>/dev/null || true)" in
	on:*)
		was_win=0
		! windows_serving || was_win=1
		[ "$was_win" = 0 ] || stop_windows
		"$BIN" autostart on
		[ "$was_win" = 0 ] || start_windows
		;;
	*)
		echo "autostart is off; the Windows copy was not touched (garminctl autostart on)"
		;;
	esac
fi

if [ "$was_wsl" = 1 ]; then
	start_wsl
	wait_ready wsl
elif windows_serving; then
	wait_ready windows
else
	echo "No development server was running; binaries updated without starting one."
fi

"$BIN" version
