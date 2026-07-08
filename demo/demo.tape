# demo.tape — the showagent README GIF.
#
# Run from the repo root (see demo/README.md for the full recipe):
#   go build -o demo/.build/showagent ./cmd/showagent
#   vhs demo/demo.tape
#
# The recording is fully sandboxed: HOME points at the fabricated
# demo/.home (regenerated below so timestamps read "2h ago"), and PATH is
# prefixed with demo/bin so `codex` / `claude` are stubs. The recorder's
# real sessions are never read or shown.

Output demo/demo.gif

Set Shell bash
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

# --- hidden setup: fake home, stub PATH, fresh timestamps --------------------
Hide
Type `demo/fixtures/gen.sh >/dev/null 2>&1`
Enter
Type `export HOME="$PWD/demo/.home" CODEX_HOME="$PWD/demo/.home/.codex" CLAUDE_HOME="$PWD/demo/.home/.claude" JCODE_HOME="$PWD/demo/.home/.jcode"`
Enter
Type `export PATH="$PWD/demo/bin:$PWD/demo/.build:$PATH" PS1='❯ '`
Enter
Type `clear`
Enter
Sleep 500ms
Show

# --- 1. launch: grouped sessions from both agents, newest first ---------------
Type "showagent"
Sleep 500ms
Enter
Sleep 3s

# --- 2. browse: detail panel follows the cursor -------------------------------
Down
Sleep 800ms
Down
Sleep 800ms
Up
Sleep 1.2s

# --- 3. preview toggle: first message -> latest -> both ------------------------
Type "l"
Sleep 1.2s
Type "b"
Sleep 1.5s

# --- 4. search: fuzzy-find the Codex rate-limiting session --------------------
Type "/"
Sleep 500ms
Type "rate limit"
Sleep 1.5s
Enter
Sleep 1.5s

# --- 5. money shot: convert the Codex session to Claude Code ------------------
Type "x"
Sleep 3.5s

# --- 6. resume the converted session in its new agent -------------------------
Enter
Sleep 4s

# --- 7. end card ---------------------------------------------------------------
Hide
Ctrl+C
Sleep 300ms
Type `clear && printf '\n\n\n   \033[1mshowagent\033[0m — every AI coding session on your machine, in one TUI\n\n   go install github.com/aytzey/showagent/cmd/showagent@latest\n\n'`
Enter
Sleep 300ms
Show
Sleep 3s
