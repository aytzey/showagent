# Sample demo

The VHS recording illustrates **Codex → Claude conversation conversion** with
fabricated sessions. Its final CLI screen is explicitly simulated. It does not
prove that a real Claude/Codex CLI loaded the copy or that a model continued the
conversation; those require separate, versioned compatibility checks.

## Run it safely

From the repository root on Linux/macOS:

```sh
mkdir -p demo/.build
go build -o demo/.build/showagent ./cmd/showagent
bash demo/run-isolated.sh             # sample TUI
bash demo/run-isolated.sh list --json # sample sessions, no real agent needed
bash demo/run-isolated.sh --shell     # isolated shell used by the recording
```

`SHOWAGENT_DEMO_BINARY` can point to an existing built binary. The runner creates
a **new owned directory** under `demo/.runs` each time and prints its location.
Generated directories are kept for inspection, gitignored, and never recursively
deleted by the runner. The generator refuses an existing destination, including
an existing directory containing agent files.

The child environment is rebuilt with `env -i`: HOME, USERPROFILE, AppData/XDG,
temporary files, all six provider stores, both Pi overrides, and the learnings directory point
inside the sample root. Update checks are disabled. The working directory also
changes into that root, so caller-local Pi settings are not read. PATH contains
only the two demo stubs; an installed real `pi`, `opencode`, or other agent cannot
become a target. Required Bash/system helpers are resolved before isolation.

The native Windows binary can be used for CLI fixture smoke checks from Git
Bash. The VHS recording and POSIX executable stubs target Linux/macOS; this is
not a Windows native-resume test.

## Record the illustration

```sh
vhs demo/demo.tape
```

Requires VHS, ttyd, and ffmpeg, plus GNU date for fixtures. On macOS install
coreutils or set `DATE_BIN=gdate`; the generator detects `gdate` automatically.
The tape uses the same isolated runner as the manual demo. It opens with the
Codex → Claude outcome, previews and confirms the conversion, then hands the
resume command to an explicitly labeled stub. No fabricated model answer or
restored-message count is shown.

The intended loop is about 15–25 seconds at 1200×700, with a GIF budget of 3 MB.
After recording, inspect `docs/demo.gif` at its actual display size. Keep the
label **“Sample sessions; simulated CLI output”** beside any published embed.
Editing a tape does not regenerate or verify the existing media asset.

## Fixtures and regression checks

`fixtures/gen.sh` writes 5 Codex, 5 Claude, and 2 Gemini sessions across three
sample workspaces. It JSON-escapes paths and messages, including quotes,
backslashes, control characters, and Unicode. To inspect fresh fixtures:

```sh
bash demo/fixtures/gen.sh # prints a new owned root; never reuses demo/.home
NOW=2026-09-06T12:00:00Z bash demo/fixtures/gen.sh # fixed timestamps
```

Python 3.9+ runs the regression checks. A built binary enables the full launcher
smoke test; without one those checks are explicitly skipped.

```sh
SHOWAGENT_DEMO_BINARY="$PWD/demo/.build/showagent" python3 demo/test_demo.py -v
shellcheck demo/run-isolated.sh demo/fixtures/gen.sh demo/bin/*
shellcheck --shell=bash demo/shell.rc
```

The tests use only owned synthetic directories. They check refusal to overwrite
existing data, JSON paths, foreign Pi overrides, and source preservation during
preview/conversion. They do not invoke real agent CLIs or model accounts.
