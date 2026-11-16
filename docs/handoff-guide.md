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

To choose an earlier point, list user messages with their absolute numbers.
`messages` shows the newest 30 by default; add `--all` to see every message:

```sh
showagent messages SOURCE_SESSION_ID
showagent messages SOURCE_SESSION_ID --all
showagent convert SOURCE_SESSION_ID --to claude --through-user 12 --dry-run
showagent convert SOURCE_SESSION_ID --to claude --before-user 12 --dry-run
```

`--through-user 12` includes user message #12 but excludes its answer and
every later turn; `--before-user 12` leaves that message out too, ending on
the reply before it so you can ask differently. They cannot be combined.
Keep the chosen flag when replacing `--dry-run` with a write command.
`--scope last:50` keeps the last 50 turns counted back from that point.
Without either cutoff flag, conversion keeps the conversation's end. Neither
choice restores earlier project files; the workspace stays in its current state.
The first user message cannot be left out when nothing precedes it.

When the preview matches your intent, remove `--dry-run`, keeping the same
cutoff and scope. For the full-conversation preview above:

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

To write the session and continue in one step, use `--open` in place of
`--dry-run` once the preview matches your intent (showagent v0.16.0 or newer):

```sh
showagent convert SOURCE_SESSION_ID --to claude --scope all --open
```

`--open` first checks that the `claude` CLI is on `PATH` and that the
workspace exists; if either is missing it stops before writing anything. It
then writes the new session and opens it in Claude Code. If the launch itself
fails, the session has already been written and showagent prints its resume
recipe.

### Branch instead of handing off

A branch copies the conversation into a new session in the same agent, up to
the point you choose. If a request took the conversation in the wrong
direction, leave it out and ask differently while keeping the original:

```sh
showagent branch SOURCE_SESSION_ID --before-user 12 --dry-run
showagent branch SOURCE_SESSION_ID --before-user 12 --open
```

`branch` accepts the same `--through-user`, `--before-user`, and `--scope`
choices as `convert`. `--dry-run` writes nothing; `--open` writes the branch
and opens it in its agent. Without either flag, it writes immediately and
prints the resume recipe. Using the source provider as `convert --to` still
creates a branch; `showagent branch` is the direct command. The original
session and current project files stay as they are. Antigravity sessions
cannot be branched.

### The same steps in the picker

Select the source row, then press `o` until the target is `claude`. Press `t`
if you want a smaller hand-off scope. Press `x` for **Hand off to Claude ·
choose where it picks up**, or `n` for **Branch · choose where the copy picks up**.
Both list every user message, newest first, plus `Continue from the end`.

- `↑/↓` or `pgup/pgdn` selects a message; `/` searches by words or `#N`.
- `tab` leaves the chosen message out, ending on the preceding reply. Press
  again to include it. This is unavailable for the first message when nothing
  precedes it.
- The live `keeps X of Y turns` line shows how much will be copied;
  `ctrl+d/u` scrolls a long message.
- `enter` finishes searching. Once search is finished, `enter` creates a
  branch, or opens the hand-off preview. For a hand-off, press `x` again to
  create and select the new session.
- `esc` clears search, then cancels the chooser; it also closes the preview.

Press `enter` on the new session to resume it.
Only targets with a CLI on `PATH` appear in the picker.

To continue in another terminal pane instead, press `c` on the new row. It
copies the resume command, including the `cd` into the workspace, to the
clipboard (showagent v0.16.0 or newer).

## What moves with the conversation

| Carried into the new session | Stays with the source agent |
|---|---|
| Extracted user and assistant message text within the selected scope | Tool calls and tool results |
| Code blocks, newlines, and indentation in those messages | Permission and approval history |
| Workspace association and a new native session ID | Model/token/runtime metadata, caches, checkpoints, and subagent internals |
| | Provider attachments and encrypted reasoning state |
| | Any single session record over 16 MiB, such as a tool result with inline screenshots |

For file-backed agents, showagent writes a new private file atomically.
OpenCode and MiMoCode create the session through their own CLI. Neither conversion nor branching modifies the original session.
Branching applies the same transferable-message projection in the source
agent; it is not a byte-for-byte session backup.

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

Other target identifiers are `gemini`, `opencode`, `mimocode`, `jcode`,
and `pi`. File-backed targets can be prepared by the CLI
before their agent is installed; resuming still requires that agent.
OpenCode and MiMoCode require their CLI even when writing a converted
session, because the import goes through the agent itself.

### Antigravity

Antigravity is a hand-off source. Sessions from the `agy` terminal agent
appear in the picker with their workspace, and `enter` reopens one with
`agy --conversation <id>`. Antigravity IDE conversations appear as hand-off
sources. Convert either kind into Claude Code, Codex, Gemini CLI, or any
other supported target:

```sh
showagent convert ANTIGRAVITY_CONVERSATION_ID --to claude --dry-run
```

Antigravity sessions cannot be branched. Converting *into* Antigravity is not
available yet. `agy` has no way to create a conversation without running a
model turn, and in that turn the model can write files if the handed-off text
asks it to. Delete Antigravity conversations from `agy`'s own `/resume`
picker (`f4`).

## If something does not work

| Symptom | Next step |
|---|---|
| No sessions found | Run `showagent list` to see scanned directories and environment overrides. Start a supported local session, then press `r` in the picker to rescan. |
| Search misses a remembered sentence | Search its workspace or first/latest user prompt instead. Middle messages and assistant responses are not indexed by the picker. |
| Claude is absent from the target choices | Make the `claude` executable available on `PATH` and reopen showagent. |
| `workspace not found` | Restore access to the original workspace. Conversion does not recreate its files. |
| Conversion succeeds but native resume fails | Keep the original session. Record the showagent version, target CLI version, OS, direction, and error; omit private transcript content when reporting the issue. Consult the compatibility record. |
| A documented command is unknown | Compare `showagent --help` with the channel notes in [distribution details](distribution.md). `convert --open`, `branch`, `--before-user` on `branch` and `convert`, `messages --all`, the chooser's full message list, `/` search and `tab` leave-out, and MCP `before_user` and `get_user_messages` paging need v0.16.0. v0.11.0 does not have the standalone `transcript` command. |
