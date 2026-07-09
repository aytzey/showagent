#!/usr/bin/env bash
# Real-CLI end-to-end check for showagent.
#
# Unlike the unit suite (hermetic, env-override homes), this exercises the
# REAL agent CLIs against their REAL stores: it creates tiny seed sessions
# with `claude -p` and `codex exec` inside throwaway workspaces, then uses
# showagent's own Branch/Convert/Delete code paths on those rows, and verifies
# every artifact with the target CLI itself (claude --resume, codex exec
# resume, gemini --list-sessions, opencode export, jcode replay --export). Costs
# a few small model calls. Only sessions whose cwd is one of the throwaway
# workspaces are ever touched or deleted.
#
# Usage: scripts/e2e-real.sh   (requires: go, claude; optional: codex, gemini, opencode, jcode)
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

WS_ROOT="$(mktemp -d /tmp/showagent-e2e.XXXXXX)"
WS1="$WS_ROOT/plain"
WS2="$WS_ROOT/with space (tricky)"
MANIFEST="$WS_ROOT/manifest.json"
SEED_MARKER="$WS_ROOT/seed-started"
mkdir -p "$WS1" "$WS2"
touch "$SEED_MARKER"

PASS=0; FAIL=0; SKIP=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
skip() { SKIP=$((SKIP+1)); echo "  SKIP  $1"; }
have() { command -v "$1" >/dev/null 2>&1; }
seed_with_retry() { # cwd log command...
  local cwd="$1" log="$2"
  shift 2
  for _ in 1 2; do
    if (cd "$cwd" && "$@") >"$log" 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}
session_persisted() { # store-root cwd
  local root="$1" cwd="$2" file
  [ -d "$root" ] || return 1
  while IFS= read -r file; do
    if grep -Fq -- "$cwd" "$file"; then
      return 0
    fi
  done < <(find "$root" -type f -name '*.jsonl' -newer "$SEED_MARKER" 2>/dev/null)
  return 1
}

echo "== workspaces: $WS_ROOT"

echo "== seeding real sessions"
if have claude; then
  if seed_with_retry "$WS2" "$WS_ROOT/claude-seed.log" claude -p "Reply with exactly: E2E-SEED"; then
    ok "seed: claude session in spaced workspace"
  elif session_persisted "${CLAUDE_HOME:-${HOME}/.claude}/projects" "$WS2"; then
    ok "seed: claude persisted a native session despite model/CLI failure"
  else
    bad "seed: claude -p failed"
    tail -5 "$WS_ROOT/claude-seed.log"
  fi
else
  bad "claude CLI not installed (required)"
fi
if have codex; then
  if seed_with_retry "$WS1" "$WS_ROOT/codex-seed.log" codex exec --skip-git-repo-check "Reply with exactly: E2E-SEED"; then
    ok "seed: codex session in plain workspace"
  elif session_persisted "${CODEX_HOME:-${HOME}/.codex}/sessions" "$WS1"; then
    ok "seed: codex persisted a native session despite model/CLI failure"
  else
    bad "seed: codex exec failed"
    tail -5 "$WS_ROOT/codex-seed.log"
  fi
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
  if out="$(cd "$cwd" && claude --resume "$id" -p "Reply with exactly: E2E-RESUME-OK" 2>&1)" && echo "$out" | grep -qx "E2E-RESUME-OK"; then
    ok "claude --resume finds $id (branched, spaced cwd)"
  else
    bad "claude --resume $id: $out"
  fi
done < <(manifest_rows branched claude)

while IFS=$'\t' read -r id cwd; do
  [ -z "$id" ] && continue
  if out="$(cd "$cwd" && claude --resume "$id" -p "Reply with exactly: E2E-RESUME-OK" 2>&1)" && echo "$out" | grep -qx "E2E-RESUME-OK"; then
    ok "claude --resume finds $id (converted from codex)"
  else
    bad "claude --resume $id (converted): $out"
  fi
done < <(manifest_rows converted claude)

if have codex; then
  for kind in branched converted; do
    while IFS=$'\t' read -r id cwd; do
      [ -z "$id" ] && continue
      if out="$(cd "$cwd" && codex exec --skip-git-repo-check resume "$id" "Reply with exactly: E2E-RESUME-OK" 2>&1)" && echo "$out" | grep -qx "E2E-RESUME-OK"; then
        ok "codex exec resume finds $id ($kind)"
      elif echo "$out" | grep -qi "usage limit"; then
        skip "codex resolved $id ($kind), but account usage limit blocked the model response"
      else
        bad "codex exec resume $id ($kind): $(echo "$out" | tail -2)"
      fi
    done < <(manifest_rows "$kind" codex)
  done
fi

if have gemini; then
  while IFS=$'\t' read -r id cwd; do
    [ -z "$id" ] && continue
    out="$(cd "$cwd" && gemini --list-sessions 2>&1)"
    if echo "$out" | grep -q "$id"; then
      ok "gemini --list-sessions shows $id (converted)"
    else
      bad "gemini --list-sessions missing $id: $(echo "$out" | tail -3)"
    fi
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

if have jcode; then
  while IFS=$'\t' read -r id cwd; do
    [ -z "$id" ] && continue
    out="$(cd "$cwd" && jcode replay --no-update --export "$id" 2>&1)"
    if echo "$out" | grep -q "E2E-SEED"; then
      ok "jcode replay exports $id with the converted transcript"
    else
      bad "jcode replay --export $id: $(echo "$out" | tail -3)"
    fi
  done < <(manifest_rows converted jcode)
fi

echo "== cleanup via showagent Delete"
if SHOWAGENT_E2E_WS_LIST="$WS1:$WS2" SHOWAGENT_E2E_MANIFEST="$MANIFEST" \
   go test -tags realcli -run TestRealCLICleanup -count=1 -v ./internal/session/ >"$WS_ROOT/cleanup.log" 2>&1; then
  ok "cleanup: every artifact deleted through showagent"
else
  bad "cleanup failed — see $WS_ROOT/cleanup.log"
  tail -20 "$WS_ROOT/cleanup.log"
fi

manifest_all() { # -> "provider<TAB>id<TAB>file<TAB>cwd" lines
  python3 - "$MANIFEST" <<'EOF'
import json, sys
for a in json.load(open(sys.argv[1])):
    print(a["provider"] + "\t" + a["id"] + "\t" + a["file"] + "\t" + a["cwd"])
EOF
}

echo "== verifying deletions stuck"
while IFS=$'\t' read -r provider id file cwd; do
  [ -z "$id" ] && continue
  case "$provider" in
    opencode)
      if opencode export "$id" >/dev/null 2>&1; then
        bad "opencode still has $id after delete"
      else
        ok "opencode session $id gone"
      fi ;;
    jcode)
      base="${file%.json}"
      if [ -e "$file" ] || [ -e "${base}.bak" ] || [ -e "${base}.journal.jsonl" ]; then
        bad "jcode still has an artifact for $id after delete"
      else
        ok "jcode session $id and sidecars gone"
      fi ;;
    gemini)
      if [ -e "$file" ]; then
        bad "gemini file still exists: $file"
      else
        chats_dir="$(dirname "$file")"
        project_dir="$(dirname "$chats_dir")"
        rmdir "$chats_dir" 2>/dev/null || true
        if [ -f "$project_dir/.project_root" ] &&
           [ "$(cat "$project_dir/.project_root")" = "$cwd" ] &&
           [ -z "$(find "$project_dir" -mindepth 1 -maxdepth 1 ! -name .project_root -print -quit 2>/dev/null)" ]; then
          rm -f "$project_dir/.project_root"
          rmdir "$project_dir" 2>/dev/null || true
        fi
        ok "gemini file gone: $(basename "$file")"
      fi ;;
    *)
      if [ -e "$file" ]; then
        bad "$provider file still exists: $file"
      else
        ok "$provider file gone: $(basename "$file")"
      fi ;;
  esac
done < <(manifest_all)

echo
echo "== RESULT: $PASS pass, $SKIP skip, $FAIL fail (workspaces kept at $WS_ROOT for inspection)"
[ "$FAIL" -eq 0 ]
