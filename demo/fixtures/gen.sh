#!/usr/bin/env bash
# gen.sh — fabricate a believable fake HOME for the demo recording.
#
# Creates Codex sessions (rollout-*.jsonl under .codex/sessions/YYYY/MM/DD/),
# Claude Code sessions (.claude/projects/<slug>/*.jsonl), and Gemini CLI
# sessions (.gemini/tmp/<slug>/chats/session-*.json) in the exact shapes
# internal/session/codex.go, claude.go, and gemini.go parse, spread across
# three fake workspaces. Every name, path, and message is fabricated; no real
# personal data.
#
# Usage:
#   demo/fixtures/gen.sh [FAKEHOME]
#
#   FAKEHOME defaults to demo/.home (next to this script's parent dir).
#   NOW=2026-07-08T12:00:00Z pins "now" for reproducible timestamps;
#   it defaults to the current UTC time so previews read "2h ago".
#
# Requires GNU date (on macOS: brew install coreutils and use gdate).

set -euo pipefail

DATE_BIN="${DATE_BIN:-date}"
if ! "$DATE_BIN" -u -d "@0" +%s >/dev/null 2>&1; then
	if command -v gdate >/dev/null 2>&1; then
		DATE_BIN=gdate
	else
		echo "gen.sh: GNU date is required (set DATE_BIN or install coreutils)" >&2
		exit 1
	fi
fi

demo_dir="$(cd "$(dirname "$0")/.." && pwd)"
FAKEHOME="${1:-$demo_dir/.home}"
NOW="${NOW:-$("$DATE_BIN" -u +%Y-%m-%dT%H:%M:%SZ)}"
NOW_EPOCH="$("$DATE_BIN" -u -d "$NOW" +%s)"

# Timestamp helpers. The argument is "seconds before NOW".
iso() { "$DATE_BIN" -u -d "@$((NOW_EPOCH - $1))" +%Y-%m-%dT%H:%M:%SZ; }
daydir() { "$DATE_BIN" -u -d "@$((NOW_EPOCH - $1))" +%Y/%m/%d; }
filestamp() { "$DATE_BIN" -u -d "@$((NOW_EPOCH - $1))" +%Y-%m-%dT%H-%M-%S; }

# Fake workspaces (inside FAKEHOME so the demo never leaves its sandbox).
# They must exist on disk: resume chdirs into the workspace before exec.
WS_API="$FAKEHOME/code/api-server"
WS_WEB="$FAKEHOME/code/webapp"
WS_DOT="$FAKEHOME/dotfiles"

# Start clean, but only remove what we own.
rm -rf "$FAKEHOME/.codex" "$FAKEHOME/.claude" "$FAKEHOME/.jcode" "$FAKEHOME/.gemini"
mkdir -p "$WS_API" "$WS_WEB" "$WS_DOT" \
	"$FAKEHOME/.codex/sessions" "$FAKEHOME/.claude/projects" "$FAKEHOME/.jcode" \
	"$FAKEHOME/.gemini/tmp"

# codex_session AGE UUID CWD role:text...
#
# AGE is seconds-before-NOW of the LAST message; earlier records are spaced
# 90s apart before it, and the session_meta record (plus the filename stamp)
# marks the start. Message texts must be JSON-safe: no double quotes or
# backslashes (use apostrophes).
codex_session() {
	local age="$1" uuid="$2" cwd="$3"
	shift 3
	local count=$#
	local start=$((age + count * 90))
	local dir="$FAKEHOME/.codex/sessions/$(daydir "$start")"
	local file="$dir/rollout-$(filestamp "$start")-$uuid.jsonl"
	mkdir -p "$dir"

	printf '{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"%s"}}\n' \
		"$(iso "$start")" "$uuid" "$cwd" >"$file"

	local index=0 role text t kind
	for entry in "$@"; do
		index=$((index + 1))
		t=$((age + (count - index) * 90))
		role="${entry%%:*}"
		text="${entry#*:}"
		if [ "$role" = "u" ]; then
			kind=user
			printf '{"timestamp":"%s","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}}\n' \
				"$(iso "$t")" "$text" >>"$file"
		else
			printf '{"timestamp":"%s","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"%s"}]}}\n' \
				"$(iso "$t")" "$text" >>"$file"
		fi
	done
	echo "codex   $(iso "$age")  $cwd" >&2
}

# claude_session AGE UUID CWD role:text...
# Same conventions as codex_session; the project directory slug mirrors
# claudeProjectDir in internal/session/handoff.go ('/' -> '-').
claude_session() {
	local age="$1" uuid="$2" cwd="$3"
	shift 3
	local count=$#
	local slug="${cwd//\//-}"
	local dir="$FAKEHOME/.claude/projects/$slug"
	local file="$dir/$uuid.jsonl"
	mkdir -p "$dir"
	: >"$file"

	local index=0 role text t
	for entry in "$@"; do
		index=$((index + 1))
		t=$((age + (count - index) * 90))
		role="${entry%%:*}"
		text="${entry#*:}"
		if [ "$role" = "u" ]; then
			printf '{"type":"user","message":{"role":"user","content":"%s"},"uuid":"%s-%d","timestamp":"%s","cwd":"%s","sessionId":"%s"}\n' \
				"$text" "$uuid" "$index" "$(iso "$t")" "$cwd" "$uuid" >>"$file"
		else
			printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]},"uuid":"%s-%d","timestamp":"%s","cwd":"%s","sessionId":"%s"}\n' \
				"$text" "$uuid" "$index" "$(iso "$t")" "$cwd" "$uuid" >>"$file"
		fi
	done
	echo "claude  $(iso "$age")  $cwd" >&2
}

# gemini_session AGE UUID CWD role:text...
# Same conventions as codex_session. Writes the legacy single-document format
# (one ConversationRecord per session-*.json) that internal/session/gemini.go
# parses, with a .project_root marker mapping the project dir to its
# workspace. Roles are u -> "user" and a -> "gemini".
gemini_session() {
	local age="$1" uuid="$2" cwd="$3"
	shift 3
	local count=$#
	local start=$((age + count * 90))
	local slug="${cwd//\//-}"
	local dir="$FAKEHOME/.gemini/tmp/$slug"
	local file="$dir/chats/session-$(filestamp "$start")-${uuid%%-*}.json"
	mkdir -p "$dir/chats"
	printf '%s\n' "$cwd" >"$dir/.project_root"

	printf '{"sessionId":"%s","projectHash":"%s","startTime":"%s","lastUpdated":"%s","messages":[' \
		"$uuid" "$slug" "$(iso "$start")" "$(iso "$age")" >"$file"

	local index=0 role text t type
	for entry in "$@"; do
		index=$((index + 1))
		t=$((age + (count - index) * 90))
		role="${entry%%:*}"
		text="${entry#*:}"
		type=user
		[ "$role" = "a" ] && type=gemini
		[ "$index" -gt 1 ] && printf ',' >>"$file"
		printf '{"id":"%s-%d","type":"%s","content":"%s","timestamp":"%s"}' \
			"$uuid" "$index" "$type" "$text" "$(iso "$t")" >>"$file"
	done
	printf ']}\n' >>"$file"
	echo "gemini  $(iso "$age")  $cwd" >&2
}

H=3600
D=86400

# --- Codex sessions (5) ------------------------------------------------------

# The search target of the demo: "/ rate limit" should land here.
codex_session $((2 * H)) "1f7c9a2e-4b31-4c8e-9d02-8a5e3f6b1c44" "$WS_API" \
	"u:Add rate limiting to POST /v1/charges - token bucket per API key, backed by Redis" \
	"a:Added internal/ratelimit with a token bucket keyed by API key and wired it into the charges handler as middleware." \
	"u:429 responses should include a Retry-After header and a JSON error body" \
	"a:Done - the middleware now writes Retry-After and a rate_limited error object. Added handler tests for both paths." \
	"u:the redis TTL test is flaky - mock the clock instead of sleeping"

codex_session $((30 * H)) "7d3e5b90-12af-4e67-8c41-2b9f0d6a3e58" "$WS_API" \
	"u:Migrate the orders table to UUID primary keys without downtime" \
	"a:Drafted a three-step plan: add the uuid column, backfill in batches, then swap the primary key. First migration is written." \
	"u:backfill in batches of 500 with a 100ms pause so replication lag stays low" \
	"a:Backfill job updated - 500 rows per batch, 100ms sleep, and it checkpoints the last id so it can resume."

codex_session $((8 * H)) "c2b8f1d4-6a09-4b3c-a7e5-91d20c4f8b36" "$WS_WEB" \
	"u:The Cypress login spec fails on CI about once in five runs - find the race" \
	"a:The spec asserts on the dashboard before the session cookie lands. Added an intercept that waits for /api/session first." \
	"u:nice - apply the same wait helper to the signup spec"

codex_session $((50 * H)) "9e4a7c15-3d82-4f6b-b0c9-5e1f8a2d7c03" "$WS_WEB" \
	"u:next build runs out of memory on CI - figure out what ballooned the bundle" \
	"a:Two culprits: the full moment locale bundle and a client-side import of the admin charts. Split both out; peak RSS dropped 40 percent." \
	"u:add a bundle-size check to CI so this cannot regress"

codex_session $((3 * D)) "5a1d9f60-8e24-4c7b-9b3a-0f6c2e8d4a17" "$WS_DOT" \
	"u:Port my zsh aliases and prompt to fish" \
	"a:Converted the aliases to fish abbreviations and rewrote the prompt in fish_prompt.fish with git status support." \
	"u:the git branch segment should render dim when the tree is clean"

# --- Claude Code sessions (5) ------------------------------------------------

claude_session $((4 * H)) "e8c2a94f-1b57-4d30-86ae-f42d9b71c605" "$WS_WEB" \
	"u:Refactor the checkout form into server components" \
	"a:Moved the address and payment steps to server components and kept the card field client-side. Form state now lives in a server action." \
	"u:the coupon field should validate on blur without a full submit" \
	"a:Added a useTransition-backed blur handler that calls the validate action and inlines the error message."

claude_session $((27 * H)) "b6f0d3a8-72c4-4e19-95b3-8a1c5e2f7d40" "$WS_API" \
	"u:Write integration tests for the webhook retry queue" \
	"a:Added a testcontainers Redis fixture and covered enqueue, exponential backoff, and the dead-letter path after five failures." \
	"u:also assert the retry count metric increments on each attempt"

claude_session $((6 * H)) "4d9b8e21-c5a3-4f76-b1e8-3c0a6f9d2e54" "$WS_WEB" \
	"u:Add dark mode - respect prefers-color-scheme and add a manual toggle" \
	"a:Added CSS custom properties for both palettes, a data-theme attribute on html, and a toggle that persists to localStorage." \
	"u:flash of wrong theme on first paint - inline the theme script in head"

claude_session $((5 * H)) "a3e7c1f9-04d6-4b28-8f5c-7b9e2d4a6c31" "$WS_API" \
	"u:The payment webhook handler double-charges when the provider retries - fix the idempotency" \
	"a:The handler keyed idempotency on our internal id instead of the provider event id. Switched the unique index and added a replay test." \
	"u:log a warn with the event id whenever a duplicate is dropped"

claude_session $((2 * D)) "f5b3d8c0-9a16-4e42-a7d9-1c8f4b6e0a25" "$WS_DOT" \
	"u:Set up a tmux status line that shows git branch and kubernetes context" \
	"a:Added status-right segments that shell out to git and kubectl with a 5s interval, colored by context so prod stands out." \
	"u:cache the kubectl call - it makes tmux stutter on a slow VPN"

# --- Gemini CLI sessions (2) ---------------------------------------------------

gemini_session $((7 * H)) "d1a4b7e2-58c3-4f09-9e6b-2a7d5c1f8b93" "$WS_API" \
	"u:Sketch a k6 load test for the charges endpoint - 500 rps steady with bursts to 2000" \
	"a:Wrote a k6 scenario with a constant-arrival executor at 500 rps and a ramping burst stage. Thresholds fail the run when p95 latency passes 250ms." \
	"u:tag the burst stage metrics separately so Grafana can chart them"

gemini_session $((26 * H)) "8c5f2d90-b3a7-4e14-a1c8-6f9e0b4d7a52" "$WS_DOT" \
	"u:Write a systemd user timer that snapshots my notes folder to restic every hour" \
	"a:Added restic-notes.service and a matching timer with RandomizedDelaySec so laptops on battery do not all fire at once." \
	"u:skip the snapshot when the machine is on a metered connection"

echo "fake home ready: $FAKEHOME (NOW=$NOW)" >&2
