# Sample demo

The VHS recordings illustrate **Codex → Claude conversation conversion** and
**branching before a user message** with fabricated sessions. Their final CLI
screens are explicitly simulated. They do not
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
temporary files, the provider stores, both Pi overrides, and the learnings directory point
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
vhs demo/branch.tape
```

`demo.tape` produces `docs/demo.gif` and `docs/demo.mp4`: choose where to continue,
preview the Codex → Claude conversion, confirm it, and resume the copy.
`branch.tape` produces `docs/branch.gif`: search for the sliding-window wrong
turn, leave user #4 out, and resume the new Codex branch while keeping the original.

The `demo.yml` GitHub workflow records both on Linux and uploads the three files
in the `sample-demo` artifact for visual review; it never commits or publishes
them. Open **Actions → demo → a completed run → Artifacts → sample-demo** to
download them, or run `gh run download <run-id> -n sample-demo`. Trigger a run
manually with `gh workflow run demo.yml --ref <branch>`. Use the MP4 when a
publishing channel needs video.

Requires VHS, ttyd, and ffmpeg, plus GNU date for fixtures. On macOS install
coreutils or set `DATE_BIN=gdate`; the generator detects `gdate` automatically.
Both tapes use the same isolated runner as the manual demo and hand resume
commands to explicitly labeled stubs. No fabricated model answer or restored-message
count is shown.

The intended loop is about 15–25 seconds at 1200×700, with a GIF budget of 3 MB.
After recording, inspect both GIFs at their actual display size. Keep the
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
preview/conversion and branching before user #4. They also check isolated startup
for every tape. They do not invoke real agent CLIs or model accounts.
