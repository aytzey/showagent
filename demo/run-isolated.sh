#!/usr/bin/env bash
# A fresh sample store, without inherited agent homes or executables.
set -euo pipefail

demo_dir="$(cd "$(dirname "$0")" && pwd -P)"
showagent_bin="${SHOWAGENT_DEMO_BINARY:-$demo_dir/.build/showagent}"
if [ ! -f "$showagent_bin" ] || [ ! -x "$showagent_bin" ]; then
	echo "demo: build demo/.build/showagent first, or set SHOWAGENT_DEMO_BINARY" >&2
	exit 1
fi
showagent_bin="$(cd "$(dirname "$showagent_bin")" && pwd -P)/$(basename "$showagent_bin")"
bash_bin="$(command -v bash)"
env_bin="$(command -v env)"
demo_root="$("$bash_bin" "$demo_dir/fixtures/gen.sh")"
demo_root="$(cd "$demo_root" && pwd -P)"
case "$demo_root" in
	"$demo_dir"/.runs/showagent-demo.*) ;;
	*) echo "demo: generated root escaped demo/.runs" >&2; exit 1 ;;
esac
if [ "$(<"$demo_root/.showagent-demo-owned")" != 'showagent-demo-v1' ]; then
	echo "demo: generated root lacks its ownership marker" >&2
	exit 1
fi

native_root="$demo_root"
if command -v cygpath >/dev/null 2>&1; then native_root="$(cygpath -m "$demo_root")"; fi
printf 'Sample sessions; simulated CLI output. No model is called.\n' >&2
printf 'Demo fixtures: %s\n' "$native_root" >&2
# Keep the owned directory for inspection; no recursive deletion is performed.
# Running from here also excludes the caller's project-local Pi settings.
cd "$demo_root"
command=("$showagent_bin" "$@")
if [ "${1:-}" = '--shell' ]; then
	[ "$#" -eq 1 ] || { echo "usage: run-isolated.sh --shell" >&2; exit 2; }
	command=("$bash_bin" --noprofile --rcfile "$demo_dir/shell.rc" -i)
fi

"$env_bin" -i \
	HOME="$native_root" USERPROFILE="$native_root" \
	APPDATA="$native_root/AppData/Roaming" LOCALAPPDATA="$native_root/AppData/Local" \
	TMPDIR="$native_root/.tmp" TMP="$native_root/.tmp" TEMP="$native_root/.tmp" \
	XDG_CONFIG_HOME="$native_root/.config" XDG_CACHE_HOME="$native_root/.cache" \
	XDG_DATA_HOME="$native_root/.local/share" XDG_STATE_HOME="$native_root/.local/state" \
	CODEX_HOME="$native_root/.codex" CLAUDE_HOME="$native_root/.claude" \
	JCODE_HOME="$native_root/.jcode" GEMINI_CLI_HOME="$native_root" \
	OPENCODE_DATA_HOME="$native_root/.local/share/opencode" \
	PI_CODING_AGENT_DIR="$native_root/.pi/agent" \
	PI_CODING_AGENT_SESSION_DIR="$native_root/.pi/agent/sessions" \
	SHOWAGENT_LEARNINGS_DIR="$native_root/.showagent/learnings" \
	SHOWAGENT_NO_UPDATE_CHECK=1 \
	SHOWAGENT_DEMO_BINARY="$showagent_bin" \
	PATH="$demo_dir/bin" TERM="${TERM:-xterm-256color}" \
	LANG="${LANG:-C.UTF-8}" COLORTERM="${COLORTERM:-truecolor}" \
	"${command[@]}"
