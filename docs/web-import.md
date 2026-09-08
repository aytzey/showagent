# Import a ChatGPT or Claude conversation

showagent can turn an accessible ChatGPT or Claude share into a native coding-agent session. You can also paste or pipe conversation text, which keeps private conversations private.

An import carries the visible user and assistant text. It does not download attachments, restore files, copy tool calls, transfer permissions, or start a model response.

## Preview and create a session

Use explicit speaker labels in pasted text:

```text
User:
Keep search local and offline.

Assistant:
Use an embedded index and rebuild it incrementally.
```

Preview before writing:

```sh
showagent import --file conversation.txt --to codex --cwd ./my-project --dry-run
```

The preview shows the message count plus bounded, secret-redacted first and
last message boundaries so you can catch the wrong source before anything is
written.

Remove `--dry-run` to create the session. `--cwd` selects where the agent will work; the session itself remains in that agent's normal session store.

For a readable public share link:

```sh
showagent import --url 'https://chatgpt.com/share/…' --to claude --cwd ./my-project --dry-run
```

The URL form reads only supported HTTPS share pages. It does not use browser cookies or sign in. If a link is restricted, unavailable, or its page format is not recognized, copy the text you can access and use `--file` or `--stdin` instead. A ChatGPT `/s/…` scheduled-task share and a private `/c/…` conversation are not conversation-share inputs.

Unlabelled text is not silently split into alternating speakers. Import it as one historical context note when that is what you intend:

```sh
cat notes.txt | showagent import --stdin --as-note --to codex --cwd ./my-project
```

For automation, a versioned JSON input preserves roles without relying on display labels:

```json
{
  "schema_version": 1,
  "source_kind": "transcript_file",
  "acquired_at": "2026-09-08T12:00:00Z",
  "messages": [
    {"role": "user", "text": "Keep search local."},
    {"role": "assistant", "text": "Use an embedded index."}
  ],
  "completeness": "unknown"
}
```

Pass it with `--format json`; set `acquired_at` to the time the transcript was
captured. Use exactly one source: `--url`, `--file`, or `--stdin`.

## Add to an existing Codex session

Select the exact session ID; `latest` is deliberately not accepted for this operation:

```sh
showagent import --file conversation.txt --into codex:EXACT_SESSION_ID --dry-run
showagent import --file conversation.txt --into codex:EXACT_SESSION_ID
```

This appends the messages to the existing Codex session's model context without starting a model turn. The session ID and prior stored records are retained. Imported context may not appear as separate chat bubbles in Codex's history view.

showagent records a small local receipt without transcript text or the share
URL. Repeating the same reviewed content for the same target becomes a verified
no-op. If a previous native write may have completed but its receipt was not
committed, showagent stops instead of blindly duplicating the messages.

Existing-session import is provider-specific. showagent keeps the option unavailable for Claude Code, Gemini CLI, OpenCode, jcode, and Pi until their native interfaces can preserve the existing session safely. Creating a new session remains available for supported conversion targets.

Do not import into a session that another Codex client is actively changing. showagent detects changes between preview and apply and verifies the original file prefix after the native operation, but it cannot coordinate with every external Codex process.

## Interactive picker

Run `showagent` and press `i`, including from the empty state. Choose link or pasted text, review the parsed speakers and content boundary, then choose a new session or a supported existing-session target. For pasted text, `Ctrl+Enter` opens the preview directly. If your terminal cannot distinguish that shortcut, press `Tab` to focus **Review conversation**, then press `Enter`. Import completes before any optional resume action; imported instructions are history and are never executed during import.

## Privacy and limits

- Pasted/file input is processed locally. Link input connects directly to the supported share host; there is no scraping proxy or showagent account.
- Share URLs can carry access tokens. showagent does not put the URL into the imported transcript or normal success output.
- Import receipts live in the user state directory (override with `SHOWAGENT_STATE_DIR`) and contain hashes, counts, target identity, and native revisions, but no transcript text, source URL, or project path.
- HTML, inert React Router payloads, and same-origin public snapshot JSON are parsed as data. Page scripts are not executed, redirects are revalidated, and local/private network destinations are rejected.
- Oversized pages, text, or message lists fail instead of being silently truncated.
- Use `--json` for a machine-readable preview or receipt. The output contains target metadata and counts, not the imported transcript or share URL.
- `native_history_visible` describes existing-session append behavior. It is `false` for new-session imports because that capability flag does not apply to a newly created transcript.

See [compatibility evidence](compatibility.md) for the distinction between format conversion, native loading, and model continuation.
