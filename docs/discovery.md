# Discovery and caching

showagent reads each agent's own session store. This page describes how the
picker fills, what is cached, and how to reset it.

## The picker fills as scans finish

The picker shows sessions as each agent's scan finishes. A first scan of a
large archive can take longer; agents that are ready remain usable while the
others load. Press `r` to rescan in place; the cursor, search, and filters are
kept.

A missing or malformed session preview is skipped while healthy sessions
remain visible. Leaving the picker cancels unfinished read-only database scans
and cleans up their temporary output.

## What is cached

| Agent | Cache behavior |
|---|---|
| Codex, Claude Code | Session summaries are cached by file identity, change time, size, and modification time. |
| OpenCode, MiMoCode | Listings refresh when the agent's database or its write-ahead log changes. |
| OpenCode 2.x | Unchanged sessions reuse their cached previews after a database update. |

Private metadata caches redact common secret patterns and live in the OS cache
directory under `showagent/discovery-v3` (`~/.cache/showagent/discovery-v3` on
Linux). Deleting this directory is safe: the next scan rebuilds it. Deleting a
session in the picker also removes its cached preview.

## OpenCode versions

OpenCode's installed version selects the adapter:

- **Before 2.x:** showagent reads sessions through OpenCode's database CLI.
- **2.x:** showagent uses OpenCode's global paginated API and its native
  session import and export commands. API reads may start OpenCode's managed
  background service.
- **Unknown versions** are skipped during discovery and produce an error for
  transcript or conversion operations.

Both paths run through your own `opencode` CLI; showagent never writes into
the database directly. The [compatibility record](compatibility.md) lists the
OpenCode versions that have been checked.

## When a session is missing

Run `showagent list`. With no sessions found, it prints exactly which
directories were scanned and which environment variables override them. The
[supported agents table](../README.md#supported-agents) lists every store and
its override.
