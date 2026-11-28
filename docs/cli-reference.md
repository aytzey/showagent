# showagent CLI and picker reference

For installation and a first hand-off, see the [README](../README.md#quick-start).
Run `showagent --help` for the commands available in your installed version.

## Scripting

`showagent list --json` returns an array sorted by most recent update first.
The field names are a stable CLI contract. `updated` is an RFC 3339 timestamp
or an empty string when the time is unknown. `first_message` and
`last_message` are user-message previews.

```json
[
  {
    "id": "1f7c9a2e-4b31-4c8e-9d02-8a5e3f6b1c44",
    "provider": "codex",
    "workspace": "/home/you/code/api-server",
    "updated": "2026-07-08T19:51:25Z",
    "first_message": "Add rate limiting to POST /v1/charges",
    "last_message": "The Redis TTL test is flaky. Mock the clock."
  }
]
```

The plain `showagent list` exits with status 1 when it finds no sessions.
`showagent list --json` returns `[]` with status 0 in that case. When stdout is
redirected or piped, calling `showagent` with no arguments behaves like the
plain list command. Exit codes are 0 for success, 1 for an error, and 2 for a
usage error.

Use `latest` in commands that accept a session ID to select the most recently
updated session across all agents and workspaces. Use an explicit ID across
multiple commands when the source must stay the same.

| Command | Output or action |
|---|---|
| `showagent info SESSION_ID` | Exact resume command, working directory, and storage location |
| `showagent resume SESSION_ID` | Open the session directly in its agent |
| `showagent transcript SESSION_ID --max-turns 50 --json` | Recent user and assistant turns for scripts, with redaction and a historical-data warning |
| `showagent transcript SESSION_ID --markdown` | Readable transcript with the same redaction and warning |
| `showagent messages SESSION_ID --all` | User messages, newest first, with absolute message numbers |

`transcript` reads up to 50 turns by default and caps `--max-turns` at 500.
`messages` shows the latest 30 user messages by default. Add `--all` to list
every user message and `--json` for machine-readable output.

### Branch and hand-off options

Both `branch` and `convert` accept the same cutoff and scope options.

| Option | Effect |
|---|---|
| `--through-user N` | End on user message N, leaving out its reply and all later turns |
| `--before-user N` | Leave message N out too, ending on the preceding reply |
| `--scope last:N` | Keep the latest N turns counted back from the cutoff or session end |
| `--dry-run` | Preview the workspace, transfer scope, last user message, and state left behind |
| `--open` | Check the target CLI and workspace, then create and open the copy |

A turn is one user or assistant message. Choose at most one cutoff flag. With
neither, the copy reaches the conversation's end. With no `--dry-run` or
`--open`, the command writes the copy and prints its resume recipe. The
original session and current project files stay in place. Preview redaction
affects display only, so the copy can include secret-like values hidden in the
preview. See the [hand-off guide](handoff-guide.md#what-moves-with-the-conversation).

For public share links, files, stdin, and existing-session context import, see
[web conversation import](web-import.md).

## Keybindings

| Key | Action in the session picker |
|---|---|
| `↑`, `k`, `↓`, `j`, `pgup`, `pgdn` | Move through the list |
| `/` | Search agent, session ID, paths, and first or latest user prompt. Every word must match. |
| `enter` | Resume the selected session in its own agent |
| `c` | Copy the resume command with the current resume mode |
| `1` through `8` | Toggle fixed provider slots in this order. Codex, Claude Code, jcode, OpenCode, MiMoCode, Gemini CLI, Pi, Antigravity. |
| `p` | Cycle the preview between first, latest, and both messages |
| `space` | Collapse or expand the selected workspace group |
| `o` | Cycle the hand-off target |
| `t` | Cycle scope between all turns and the latest 200, 100, 50, 20, or 10 |
| `x` | Choose a hand-off cutoff, then press `enter` to preview and `x` to write the copy |
| `n` | Choose a branch cutoff, then press `enter` to create the branch in the same agent. Available for every provider except Antigravity. |
| `D` | Choose where to trim the selected session in place. See [trimming a conversation](#trimming-a-conversation). |
| `y` | Toggle the provider's permission-bypass resume mode. jcode and Pi add no flag. |
| `C` | Resume with a project-learnings prompt |
| `i` | Import a public ChatGPT or Claude share link or pasted transcript |
| `d`, `del`, `backspace` | Arm deletion. A second press confirms, and moving disarms it. See [deletion details](#deleting-a-session). |
| `r` | Rescan session stores while keeping the cursor, search, and filters |
| `?` | Toggle key hints, with a full overlay in small terminals |
| `esc` | Clear search, close the overlay, or cancel an armed delete |
| `q`, `ctrl+c` | Quit |

The copied resume command includes a change into the workspace on Linux and
macOS. If the system clipboard is unavailable, showagent asks the terminal
to copy the command and displays it. On Windows, showagent copies commands
that require no quoting and leaves out the working-directory change.

### Message chooser

After `n` or `x`, use the arrow or paging keys to choose a user message or
**Continue from the end**. Press `/` to search by words or by message number,
such as `#4`. Press `enter` to finish searching before taking an action.

Press `tab` to leave the selected message out when an earlier turn is
available. `ctrl+d` and `ctrl+u` scroll the message preview. The chooser
reports how many turns the copy keeps. Press `enter` to create the branch
or preview the hand-off. `esc` clears search, then cancels the chooser.

### Trimming a conversation

Trimming requires showagent v0.17.0 or later.
[Build from source](../README.md#building) if your picker help has no `D` key.

Close the native agent session so it stops writing to its store. Select the
conversation in showagent and press uppercase `D` to open the message chooser.
Use `/` to find a user message.

- **Claude Code, Gemini CLI, and Pi.** The default cutoff keeps the selected
  message and permanently removes its reply and later history. Press `tab`
  to include the selected message in the deletion.
- **Codex.** The cutoff removes the selected user message, its reply, and later
  turns from the active conversation. Choose the next user message to keep an
  entire exchange. Codex keeps the previous rollout file on disk.

Press `enter` for a read-only preview with the session identity and the number
of turns to remove. Press uppercase `D` on the preview screen to apply the cut,
or `esc` to go back. Keep at least one user message. The session keeps its ID and
workspace. Project changes made during the conversation still exist in the
working directory.

Codex requires paginated history, a plain `.jsonl` active rollout, and a CLI
with native thread revert. This was verified with Codex 0.171.0 using a local
sample session. The file-backed formats
are Claude Code JSONL, Gemini CLI legacy JSON and JSONL with recognized records,
and Pi v3 JSONL with a single line of ancestry. Gemini journals with message
patches and Pi sessions with branches are rejected. Trimming clears Gemini's
generated summary and memory scratchpad, which can contain deleted content.

### Deleting a session

The picker uses `codex delete --force` for Codex and the agent's
`session delete` command for OpenCode and MiMoCode. For Claude Code it removes
the session file and its matching index entry. For jcode it removes the
session file and its backup and journal files. Gemini CLI and Pi sessions are
deleted by removing their session files. Use Antigravity's own `/resume`
picker to manage Antigravity conversations.

## Compound engineering

Press `C` and pick an agent to review a solved problem and capture durable
project learnings in Markdown. Sessions resumed in another agent use the same
user and assistant message transfer as a hand-off.

Project learnings get a shared directory under
`~/.showagent/learnings/<project>/`. Set `SHOWAGENT_LEARNINGS_DIR` to use a
different root. Each supported agent can read and write the project's notes.

`showagent setup` installs missing components for the local Codex, Claude
Code, and Pi CLIs. It can install the compound-engineering plugin and, for Pi,
the `pi-subagents` and `pi-ask-user` packages. Running setup again skips
components already installed.
[Compound-engineering plugin](https://github.com/EveryInc/compound-engineering-plugin)
