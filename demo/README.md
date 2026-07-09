# Demo recording

Everything needed to (re)record the README GIF. The recording is hermetic:
it runs against a fabricated `demo/.home` and stub `codex`/`claude`
binaries, so no real sessions are read and no real agent is launched.

## Layout

| Path | Purpose |
|---|---|
| `demo/fixtures/gen.sh` | Fabricates `demo/.home`: 5 Codex + 5 Claude Code + 2 Gemini CLI sessions across 3 fake workspaces (`code/api-server`, `code/webapp`, `dotfiles`), timestamped relative to now. |
| `demo/bin/codex`, `demo/bin/claude` | Stub CLIs that print a mock "session resumed" screen, so the resume-after-convert beat lands without real agents. |
| `demo/demo.tape` | The [vhs](https://github.com/charmbracelet/vhs) script: launch, browse, preview cycle, search, preview+confirm convert (`x`, `x`), resume, end card. |
| `demo/.home`, `demo/.build` | Generated at record time; gitignored. |

## Regenerate the GIF

From the repo root:

```sh
# 1. Build the binary the tape runs (kept out of the repo tree's PATH).
go build -o demo/.build/showagent ./cmd/showagent

# 2. Record. The tape regenerates demo/.home itself so timestamps are fresh.
vhs demo/demo.tape
```

The result is written to `docs/demo.gif`. Requirements: `vhs` and `ttyd`
on PATH, plus `ffmpeg`; fixture generation needs GNU date (on macOS:
`brew install coreutils` and run with `DATE_BIN=gdate`).

If vhs is not installed locally, the container fallback works too:

```sh
docker run --rm -v "$PWD:/vhs" ghcr.io/charmbracelet/vhs demo/demo.tape
```

The social-preview card (`docs/social-preview.png`, 1280x640) is a single
frame lifted from the GIF at the convert moment:

```sh
ffmpeg -y -ss 14 -i docs/demo.gif -frames:v 1 \
  -vf "scale=-1:640:flags=lanczos,pad=1280:640:(ow-iw)/2:0:color=0x1e1e2e" \
  docs/social-preview.png
```

(Adjust `-ss` so the frame shows the freshly converted row selected.)

## Tweaking

- **Fixtures**: edit `demo/fixtures/gen.sh`. Message texts must stay
  JSON-safe (no double quotes or backslashes). Run it standalone to
  inspect the output: `demo/fixtures/gen.sh /tmp/fakehome`, then
  `HOME=/tmp/fakehome CODEX_HOME=/tmp/fakehome/.codex CLAUDE_HOME=/tmp/fakehome/.claude JCODE_HOME=/tmp/fakehome/.jcode GEMINI_CLI_HOME=/tmp/fakehome showagent list`
  prints the plain table of everything that parsed.
- **Timestamps**: pin with `NOW=2026-07-08T12:00:00Z demo/fixtures/gen.sh`
  for reproducible output; by default they are relative to the current
  time so the TUI shows "2h ago".
- **Size budget**: target < 3 MB. If the GIF comes out larger, drop the
  tape to `Set Width 1000` / `Set FontSize 18`.
- **Keybindings**: the tape encodes `/` (search), `p` (preview cycle),
  `x` (preview / confirm hand off), `enter` (resume). If bindings change in
  `internal/tui/keys.go`, update the tape before re-recording.
