#!/bin/sh
set -eu
/opt/denova/denova-container-init

# The Claude page installs updates into the persisted home. Drop that copy once
# the image ships a newer build so image updates are never shadowed.
user_claude=/data/.local/bin/claude
if [ -e "$user_claude" ] || [ -L "$user_claude" ]; then
  bundled=$(/usr/local/bin/claude --version 2>/dev/null | awk '{print $1}') || bundled=
  current=$("$user_claude" --version 2>/dev/null | awk '{print $1}') || current=
  newest=$(printf '%s\n%s\n' "$current" "$bundled" | sort -V | tail -n 1)
  if [ -z "$current" ] || [ "$newest" != "$current" ]; then
    echo "Using bundled Claude Code ${bundled}; removing persisted ${current:-broken} copy"
    rm -rf "$user_claude" /data/.local/share/claude
  fi
fi

# The Claude page proxies Denova on the published port and supervises it.
exec /opt/denova/denova-claude-page "$@"
