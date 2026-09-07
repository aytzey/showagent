# Continue a Codex conversation in Claude Code

Find the local Codex session you want, inspect what will carry over, and write
a new Claude Code session in the same workspace. showagent preserves the
original session. It transfers user and assistant message text, not the
source agent's execution state.

## Before you start

- Install showagent using the [README instructions](../README.md#install).
  Check `showagent --version` and `showagent --help`; installation channels can
  differ. See [distribution details](distribution.md).
- Have an existing local Codex conversation and a working `claude` CLI on
  `PATH`, with its normal sign-in/setup completed.
- Keep the original workspace directory available. Both agents work with the
  files currently in that directory. showagent does not copy your repository,
  restore earlier file contents, or create a git worktree.
- Check the [compatibility record](compatibility.md). A converted file being
  written successfully does not establish that every target CLI version can
  load and continue it. Windows support is experimental.

## Find the source session

Open the picker:

```sh
showagent
```

Press `/` and search for a workspace or a phrase in the first/latest user
prompt. Press `enter` to finish searching before using action keys. The
picker also matches agent, session ID, and paths. It does not search the full
transcript. Inspect the selected row's workspace and prompts to make sure it
is the conversation you intended.

For a list with complete session IDs:

```sh
showagent list
```

Choose the Codex row you want to transfer. In the commands below, replace
`SOURCE_SESSION_ID` with that row's ID. Use an explicit ID: `latest` can refer
to a newer conversation in a different workspace or agent.

```sh
showagent info SOURCE_SESSION_ID
```

This prints the current resume recipe, workspace, and storage location. It
does not launch the agent.

## Preview, then create the new session

```sh
showagent convert SOURCE_SESSION_ID --to claude --scope all --dry-run
```

Review the source, target, workspace, transferable turn count, latest user
ask, and dropped state. `--dry-run` does not write a converted session.

To transfer only recent messages, use `--scope last:50` instead of
`--scope all`. A turn here is a user or assistant message, not a pair of
messages; trimming can leave out earlier decisions. Keep the same scope in
the preview and write commands.

When the preview matches your intent, remove `--dry-run`:

```sh
showagent convert SOURCE_SESSION_ID --to claude --scope all
```

This command writes immediately; it does not ask for a second confirmation.
It creates a new session ID and prints a resume recipe. It does not launch
Claude Code. Copy the **new** ID from that output, replacing `NEW_SESSION_ID`:

```sh
showagent info NEW_SESSION_ID
showagent resume NEW_SESSION_ID
```

`resume` opens the target CLI in the session's workspace. In that CLI, check
the imported conversation and continue with your next request. The target's
normal authentication, permissions, model availability, and context limits
still apply.

### The same steps in the picker

Select the source row, then press `o` until the target is `claude`. Press `t`
if you want a smaller scope. Press `x` to preview and `x` again to create and
select the new session. Press `enter` to resume it. `esc` backs out of search
or the preview.
Only targets with a CLI on `PATH` appear in the picker.

## What moves with the conversation

| Carried into the new session | Stays with the source agent |
|---|---|
| Extracted user and assistant message text within the selected scope | Tool calls and tool results |
| Code blocks, newlines, and indentation in those messages | Permission and approval history |
| Workspace association and a new native session ID | Model/token/runtime metadata, caches, checkpoints, and subagent internals |
| | Provider attachments and encrypted reasoning state |

For file-backed agents, showagent writes a new private file atomically.
OpenCode imports use its own CLI. Neither conversion nor branching modifies
the original session. Branching applies the same transferable-message
projection in the source agent; it is not a byte-for-byte session backup.

Conversion writes the transferable conversation text to another local agent
store. Unlike the redacted `transcript` export, it is not a secret-redacted
sharing format. When you resume, the target agent may send that conversation
to its model provider as part of its normal operation.

## Reverse direction and other targets

To move a selected Claude Code session into Codex, use its explicit source
ID with the same preview/write sequence:

```sh
showagent convert SOURCE_SESSION_ID --to codex --scope all --dry-run
```

Other target identifiers are `gemini`, `opencode`, `jcode`, and `pi`.
File-backed targets can be prepared by the CLI before their agent is
installed; resuming still requires that agent. OpenCode requires its CLI
even when writing a converted session, because the import goes through
OpenCode itself.

## If something does not work

| Symptom | Next step |
|---|---|
| No sessions found | Run `showagent list` to see scanned directories and environment overrides. Start a supported local session, then press `r` in the picker to rescan. |
| Search misses a remembered sentence | Search its workspace or first/latest user prompt instead. Middle messages and assistant responses are not indexed by the picker. |
| Claude is absent from the target choices | Make the `claude` executable available on `PATH` and reopen showagent. |
| `workspace not found` | Restore access to the original workspace. Conversion does not recreate its files. |
| Conversion succeeds but native resume fails | Keep the original session. Record the showagent version, target CLI version, OS, direction, and error; omit private transcript content when reporting the issue. Consult the compatibility record. |
| A documented command is unknown | Compare `showagent --help` with the channel notes in [distribution details](distribution.md). In particular, v0.11.0 does not have the standalone `transcript` command. |
