#!/usr/bin/env bash
# Runs a command while holding the lock on the shared test services.
#
#   scripts/with-test-lock.sh make check-unlocked
#
# The Postgres, LiveKit and port the tests use are shared by every checkout of
# this repository on the machine, worktrees included: two runs at once drop each
# other's test database and fight over the server's port. So whatever touches
# them runs under one lock, a file in the repository's shared git directory (the
# same file for every worktree), or TEAMUP_TESTS_LOCK if set.
#
# It is re-entrant: the command runs with TEAMUP_TESTS_LOCK_HELD naming the lock,
# and anything it starts that asks for the same lock again goes straight on
# rather than waiting for itself.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 command [args...]" >&2
  exit 2
fi

LOCK="${TEAMUP_TESTS_LOCK:-}"
if [ -z "$LOCK" ]; then
  common="$(git -C "$(dirname "$0")" rev-parse --git-common-dir 2>/dev/null || true)"
  if [ -n "$common" ]; then
    LOCK="$(cd "$(dirname "$0")" && cd "$common" && pwd)/teamup-tests.lock"
  else
    LOCK="${TMPDIR:-/tmp}/teamup-tests.lock"
  fi
fi

if [ "${TEAMUP_TESTS_LOCK_HELD:-}" = "$LOCK" ]; then
  exec "$@"
fi
if ! command -v flock >/dev/null 2>&1; then
  echo "note: flock is not installed, so this run does not wait for others using the test services." >&2
  exec "$@"
fi
export TEAMUP_TESTS_LOCK_HELD="$LOCK"
exec flock "$LOCK" "$@"
