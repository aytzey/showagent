#!/usr/bin/env bash
# Real-CLI end-to-end check for showagent.
#
# Unlike the unit suite (hermetic, env-override homes), this exercises the
# REAL agent CLIs against their REAL stores: it creates tiny seed sessions
# with `claude -p` and `codex exec` inside throwaway workspaces, then uses
# showagent's own Branch/Convert/Delete code paths on those rows, and verifies
# every artifact with the target CLI itself (claude --resume, codex exec
# resume, gemini --list-sessions, opencode export). Costs a few small model
# calls. Only sessions whose cwd is one of the throwaway workspaces are ever
# touched or deleted.
#
# Usage: scripts/e2e-real.sh   (requires: go, claude; optional: codex, gemini, opencode)
set -uo pipefail

cd "$(dirname "$0")/.."

WS_ROOT="$(mktemp -d /tmp/showagent-e2e.XXXXXX)"
WS1="$WS_ROOT/plain"
WS2="$WS_ROOT/with space (tricky)"
MANIFEST="$WS_ROOT/manifest.json"
mkdir -p "$WS1" "$WS2"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
have() { command -v "$1" >/dev/null 2>&1; }

echo "== workspaces: $WS_ROOT"

echo "== seeding real sessions"
if have claude; then
  (cd "$WS2" && claude -p "Reply with exactly: E2E-SEED" >/dev/null 2>&1) \
    && ok "seed: claude session in spaced workspace" \
    || bad "seed: claude -p failed"
else
  bad "claude CLI not installed (required)"
fi
if have codex; then
  (cd "$WS1" && codex exec --skip-git-repo-check "Reply with exactly: E2E-SEED" >/dev/null 2>&1) \
    && ok "seed: codex session in plain workspace" \
    || bad "seed: codex exec failed"
else
  echo "  SKIP  codex CLI not installed"
fi

echo "== branch + convert via showagent code paths"
if SHOWAGENT_E2E_WS_LIST="$WS1:$WS2" SHOWAGENT_E2E_MANIFEST="$MANIFEST" \
   go test -tags realcli -run TestRealCLIMutate -count=1 -v ./internal/session/ >"$WS_ROOT/mutate.log" 2>&1; then
  ok "mutate: branch + all available conversions"
else
  bad "mutate failed — see $WS_ROOT/mutate.log"
  tail -20 "$WS_ROOT/mutate.log"
fi

manifest_rows() { # kind provider -> "id<TAB>cwd" lines
  python3 - "$MANIFEST" "$1" "$2" <<'EOF'
import json, sys
for a in json.load(open(sys.argv[1])):
    if a["kind"] == sys.argv[2] and a["provider"] == sys.argv[3]:
        print(a["id"] + "\t" + a["cwd"])
EOF
}

echo "== verifying artifacts with the real CLIs"
while IFS=$'\t' read -r id cwd; do
  [ -z "$id" ] && continue
  out="$(cd "$cwd" && claude --resume "$id" -p "Reply with exactly: E2E-RESUME-OK" 2>&1)"
  echo "$out" | grep -q "E2E-RESUME-OK" \
    && ok "claude --resume finds $id (branched, spaced cwd)" \
    || bad "claude --resume $id: $out"
done < <(manifest_rows branched claude)

while IFS=$'\t' read -r id cwd; do
  [ -z "$id" ] && continue
  out="$(cd "$cwd" && claude --resume "$id" -p "Reply with exactly: E2E-RESUME-OK" 2>&1)"
  echo "$out" | grep -q "E2E-RESUME-OK" \
    && ok "claude --resume finds $id (converted from codex)" \
    || bad "claude --resume $id (converted): $out"
done < <(manifest_rows converted claude)

if have codex; then
  for kind in branched converted; do
    while IFS=$'\t' read -r id cwd; do
      [ -z "$id" ] && continue
      out="$(cd "$cwd" && codex exec --skip-git-repo-check resume "$id" "Reply with exactly: E2E-RESUME-OK" 2>&1)"
      echo "$out" | grep -q "E2E-RESUME-OK" \
        && ok "codex exec resume finds $id ($kind)" \
        || bad "codex exec resume $id ($kind): $(echo "$out" | tail -2)"
    done < <(manifest_rows "$kind" codex)
  done
fi

if have gemini; then
  while IFS=$'\t' read -r id cwd; do
    [ -z "$id" ] && continue
    out="$(cd "$cwd" && gemini --list-sessions 2>&1)"
    echo "$out" | grep -q "$id" \
      && ok "gemini --list-sessions shows $id (converted)" \
      || bad "gemini --list-sessions missing $id: $(echo "$out" | tail -3)"
  done < <(manifest_rows converted gemini)
fi

if have opencode; then
  while IFS=$'\t' read -r id cwd; do
    [ -z "$id" ] && continue
    if opencode export "$id" >/dev/null 2>&1; then
      ok "opencode export finds $id (converted)"
    else
      bad "opencode export cannot find $id"
    fi
  done < <(manifest_rows converted opencode)
fi

echo "== cleanup via showagent Delete"
if SHOWAGENT_E2E_WS_LIST="$WS1:$WS2" SHOWAGENT_E2E_MANIFEST="$MANIFEST" \
   go test -tags realcli -run TestRealCLICleanup -count=1 -v ./internal/session/ >"$WS_ROOT/cleanup.log" 2>&1; then
  ok "cleanup: every artifact deleted through showagent"
else
  bad "cleanup failed — see $WS_ROOT/cleanup.log"
  tail -20 "$WS_ROOT/cleanup.log"
fi

manifest_all() { # -> "provider<TAB>id<TAB>file" lines
  python3 - "$MANIFEST" <<'EOF'
import json, sys
for a in json.load(open(sys.argv[1])):
    print(a["provider"] + "\t" + a["id"] + "\t" + a["file"])
EOF
}

echo "== verifying deletions stuck"
while IFS=$'\t' read -r provider id file; do
  [ -z "$id" ] && continue
  case "$provider" in
    opencode)
      opencode export "$id" >/dev/null 2>&1 \
        && bad "opencode still has $id after delete" \
        || ok "opencode session $id gone" ;;
    *)
      [ -e "$file" ] && bad "$provider file still exists: $file" || ok "$provider file gone: $(basename "$file")" ;;
  esac
done < <(manifest_all)

echo
echo "== RESULT: $PASS pass, $FAIL fail (workspaces kept at $WS_ROOT for inspection)"
[ "$FAIL" -eq 0 ]
