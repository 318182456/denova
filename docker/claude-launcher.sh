#!/bin/sh
# Applies the token selected on the Claude page to each Claude Code launch, so
# a switch reaches the next CLI process without restarting Denova. It stays out
# of the way for API-routed runs, which must never inherit a subscription
# credential, and for commands that manage the signed-in account itself.
token_file=${HOME:-/data}/.config/denova-claude/active-token

inject=true
account_command=false
case "${1:-}" in
  setup-token) account_command=true ;;
  auth) case "${2:-}" in login | logout) account_command=true ;; esac ;;
esac
if [ "$account_command" = true ]; then
  # Account commands act on the stored sign-in, never on an injected token.
  unset CLAUDE_CODE_OAUTH_TOKEN
  inject=false
fi
if [ -n "${ANTHROPIC_API_KEY:-}${ANTHROPIC_AUTH_TOKEN:-}${ANTHROPIC_BASE_URL:-}" ]; then
  inject=false
fi
if [ "$inject" = true ] && [ -s "$token_file" ]; then
  CLAUDE_CODE_OAUTH_TOKEN=$(cat "$token_file")
  export CLAUDE_CODE_OAUTH_TOKEN
fi

# Updates from the Claude page install into the persisted home first.
for candidate in "${HOME:-/data}/.local/bin/claude" /usr/local/bin/claude; do
  if [ -x "$candidate" ]; then
    exec "$candidate" "$@"
  fi
done
echo "claude: Claude Code executable not found" >&2
exit 127
