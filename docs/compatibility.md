# Compatibility evidence

showagent implements adapters for Codex, Claude Code, Gemini CLI, OpenCode,
jcode, and Pi. An implemented adapter is not a promise that every released
CLI version, platform, and transfer direction has passed a native-resume
test. This page distinguishes the checks we can report.

## What each check proves

| Check | What a pass establishes | What it does not establish |
|---|---|---|
| Source tests | The tested source behavior passes its automated fixtures | A real provider CLI accepted a converted session |
| Cross-compile | The source builds for that OS and architecture | The binary ran there or native resume worked |
| Synthetic conversion | Sample messages produce a new session file and can be rediscovered | The target CLI loaded or continued the conversation |
| Native load/resume | A specific target CLI version opens the converted session | A model used information from the earlier conversation |
| Model continuation | A target model uses a fact supplied only in the source conversation | Complete tool/runtime-state portability or compatibility with other versions |

## Recorded checks

### Local growth-readiness build, 2026-09-07

These checks used the unreleased Windows amd64 build
`showagent growth-review-6d841e3`, based on commit `6d841e3` with the growth
readiness changes. They do not describe an already published release.

| Direction / surface | Check | Result | Scope |
|---|---|---|---|
| CLI discovery and transcript | Isolated binary smoke | Passed | Empty discovery, fabricated session, secret redaction, historical-data warning |
| Codex → Claude | Synthetic conversion | Passed | Dry-run changes no files; five user/assistant messages, Unicode and code formatting preserved |
| Claude → Codex | Synthetic return trip | Passed | Same transferable messages rediscovered; original fixture hash unchanged |
| Codex → Pi | Scoped conversion | Passed | `--scope last:2` and bounded transcript return the selected messages |
| Claude-format fixture → Codex **0.153.4** | Actual native reader | Passed | `app-server thread/read` with `includeTurns: true` returned three visible turns containing a unique decision from earlier history |
| Codex → real Claude CLI | Native loading | Not run | Claude CLI was unavailable in the isolated environment |
| Either direction | Model continuation | Not run | No model call or account credentials were used |
| Codex writer | Focused Go regression | Passed | Display events and model messages retain ordered text, including scoped/consecutive roles |
| Full Go suite on Windows | Baseline suite | Failed | Existing POSIX permissions, HOME and executable-stub assumptions fail on Windows; this is not a green full-suite claim |

The native check found a compatibility defect: response messages alone did
not populate Codex's visible history. Converted sessions now also contain the
matching user/assistant display events, without importing tool or task state.
The event contract was checked against the
[Codex 0.153.3 history builder](https://github.com/openai/codex/blob/rust-v0.153.3/codex-rs/app-server-protocol/src/protocol/thread_history.rs);
the actual passing binary check used **Codex 0.153.4**. This is a native reader
check of synthetic conversation data, not a real-source-CLI or model-resume test.

### Earlier installed binary, 2026-09-06

The earlier synthetic checks below used an installed
**showagent v0.11.0 Windows amd64 binary**, not a build of the current source.
All provider roots, including Pi's session root, were redirected to fabricated
fixtures; no real agent CLI, user session, or model call was used.

| Artifact / direction | Check | Result | Scope |
|---|---|---|---|
| v0.11.0 / Windows amd64 | Empty `list --json` | Passed | Exit 0 and an empty array |
| v0.11.0 / sample Codex session | Discovery and preview | Passed | Fixture discovered; password-like preview value redacted |
| v0.11.0 / Codex → Claude | `convert --dry-run` | Passed | Five transferable turns reported; no session copy written |
| v0.11.0 / Codex → Claude | Synthetic conversion | Passed | New file written and rediscovered; original SHA256 unchanged |
| v0.11.0 / Codex → Pi | Synthetic conversion | Passed | New file written and rediscovered; resume recipe printed |
| v0.11.0 / Windows amd64 | Standalone `transcript` command | Unsupported | Exit 2; this build does not contain that command |
| v0.11.0 / Codex → Claude or Pi | Native load and model continuation | Not run | No real target CLI was launched |

The fixture workspace included spaces and an apostrophe. This is evidence for
the listed sample case, not a platform-wide compatibility certification.
Installation channel versions and available commands are documented in
[distribution details](distribution.md).

The CI run for source commit
[`6d841e3abf3721e28ffdcb70bfd952be8e2cfb3c`](https://github.com/aytzey/showagent/commit/6d841e3abf3721e28ffdcb70bfd952be8e2cfb3c)
[passed](https://github.com/aytzey/showagent/actions/runs/34042175666).
That run used Ubuntu for automated tests and cross-compiled the published
targets. It does not certify later changes or native provider compatibility
on Linux, macOS, or Windows. Windows remains **experimental**.

## The README demo

The existing [README GIF](demo.gif) is an illustrated workflow using sample
sessions and simulated agent output. Its resume screen and model response
come from demo stubs. It is not evidence that a real model loaded or continued
an imported conversation. See the [recording instructions](../demo/README.md).

## Recording a native check

The reusable smoke harness creates its own temporary stores and strips inherited
credentials, executable paths, and provider overrides. It never calls a model:

```sh
go build -o showagent ./cmd/showagent
python3 scripts/verify-handoff.py --binary ./showagent --report handoff-result.json
# Optional: use an absolute path to an actual Codex executable for its local reader.
python3 scripts/verify-handoff.py --binary ./showagent --codex /absolute/path/to/codex
```

On Windows use the built `.exe` paths. The optional reader's version and result
are recorded separately from synthetic conversion. Keep generated reports local;
they are evidence for the tested build, not a compatibility certification.

Record the showagent commit/version, OS/architecture, source and target CLI
versions, date, direction, command, and outcome separately for each check.
Keep `passed`, `failed`, `skipped`, and `not run` distinct.

1. Create an owned temporary workspace and isolate every provider home/session
   root, including both Pi overrides and any project-local session settings.
   Disable showagent's startup update check. Do not point discovery or cleanup
   at personal agent stores.
2. Create a synthetic conversation through the source CLI. Include a unique
   decision value that is safe to publish. Record the source file's hash.
3. Preview and convert that explicit source session. Check the carried message
   count, new session ID, and unchanged original hash.
4. Open the new session with the real target CLI. Record this as a native-load
   check separately from a model call.
5. To test continuation, ask the target for the earlier decision value without
   supplying it again. Capture the actual response and exact provider version.
6. Clean up only files under the resolved, owned temporary roots. Report a
   missing CLI, unavailable isolated sign-in, or model limit as such; do not
   substitute a scripted response.

The existing [`scripts/e2e-real.sh`](../scripts/e2e-real.sh) predates this
isolation requirement: it creates and deletes test sessions in real provider
stores. Do not run it as an isolated check without adapting its storage and
cleanup boundaries. Its presence is not a recorded test result.

## Conversion boundaries

Conversion carries extracted user and assistant messages within the selected
scope, preserving their text formatting. Tool calls/results, permission and
approval state, provider attachments, encrypted reasoning, and other runtime
metadata are not transferred. Both agents use the existing workspace; files
and git history are not copied or restored. See the
[handoff guide](handoff-guide.md) for the complete preview/write/resume flow.
