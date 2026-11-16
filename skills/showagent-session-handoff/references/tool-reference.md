# showagent MCP tool reference

The MCP server runs locally over stdio. It has no hosted account, telemetry, or showagent upload path. The connected coding agent may send transcript text returned by `get_transcript` to its own model provider. Common secret-like values are redacted by default; this cannot guarantee that all personal, proprietary, or secret data is removed.

## Tools

| Tool | Input | Effect |
|---|---|---|
| `list_sessions` | Optional `provider`, `workspace`, `query`, `limit` | Read-only metadata and first/last user-message previews; newest first. Default limit 25, maximum 100. |
| `get_transcript` | Required exact `id`; optional `max_turns` | Reads recent user/assistant turns. Default 50, maximum 500. Secret-like values are redacted unless the server was explicitly started with `--allow-secrets`; do not enable that option. |
| `get_user_messages` | Required exact `id` | Lists the last 30 user messages, newest first, with absolute one-based `number` values. Always redacts common secrets. |
| `preview_conversion` | Required exact `id`, `target`; optional `through_user`, `max_turns` | Read-only dry run of a branch or conversion. Reports `scope`, `transfer_turns`, the `last_user` message in the copied range, what a hand-off leaves behind (`dropped`), and `warnings` such as a missing target CLI. Writes nothing; always redacts common secrets. Pass the session's own provider as `target` to preview a branch. |
| `resume_command` | Required exact `id` | Returns a command and working directory. It does not execute the command. |
| `branch_session` | Required exact `id`; optional `through_user`, `max_turns` | Writes a new native session for the same provider. It does not change the source. Requires the default writable server and explicit user confirmation. |
| `convert_session` | Required exact `id`, `target`; optional `through_user`, `max_turns` | Writes a new native session for a different provider. It does not change the source. Requires the default writable server and explicit user confirmation. |

Valid provider IDs include `codex`, `claude`, `gemini`, `antigravity`, `opencode`, `mimocode`, `jcode`, and `pi`. `antigravity` sessions can be converted from, but not into. Conversion to OpenCode or MiMoCode requires that provider's CLI because showagent uses the CLI to import.

Antigravity cannot receive a new session, so `branch_session` is unsupported for every Antigravity session. Antigravity IDE sessions also cannot be natively resumed; the read-only `resume_command` tool returns an error for those sessions. A successful resume command is required before offering native resume.

`get_transcript` reports `total_turns`, `truncated`, and `secrets_redacted`. Before reading, verify the live tool description says the server redacts secret-like values; a server started with `--allow-secrets` instead advertises verbatim output. Do not rely on the response flag to prevent an unsafe read.

There is no delete tool. Branch and conversion preserve the original and create a separate session. They carry transferable user/assistant turns, not provider-private tool calls, approvals, encrypted reasoning, or attachments. Ask where to continue using `get_user_messages`: pass the selected absolute `number` as `through_user`. That user message and its preceding context are copied; its answer and all later turns are excluded. Zero or omitted means the full conversation. Project files are never rewound.

`max_turns` means two different things. On `get_transcript` it only limits how much is read. On `preview_conversion`, `branch_session`, and `convert_session` it is a copy scope: only that many most recent turns are carried over, counted back from the `through_user` cutoff when one is set; zero or omitted copies every turn, and a negative value is refused. Run `preview_conversion` with the scope you intend to write, show what it reports, then pass the same `through_user` and `max_turns` to the write tool. Before confirmation, explain the selected cutoff and any earlier, unpreviewed context included in the copy.

`preview_conversion` and the write tools' `max_turns` parameter were added in showagent v0.16.0. If the connected server does not list them, a last-N copy and a dry run are not available through MCP: say so and ask the user to update showagent and reconnect.

Redaction affects only read output, including the `preview_conversion` dry run. Both `branch_session` and `convert_session` copy original transferable content, including secret-like values hidden in the preview. Before either write is confirmed, explain that those values may enter the new session and its target model's context. Neither MCP handoff tool provides sanitized-only copying.

## Local setup

Install showagent using the instructions in the [showagent README](https://github.com/aytzey/showagent#install), then configure the MCP server in the coding agent that will use it. Use that agent's current MCP setup guide; configuration file locations vary by agent and version.

For read-only access, the command is:

```sh
showagent mcp --read-only
```

For clients that have an MCP config with a command and argument array, the equivalent is:

```json
{
  "mcpServers": {
    "showagent": {
      "command": "showagent",
      "args": ["mcp", "--read-only"]
    }
  }
}
```

The read-only mode exposes search, transcript, hand-off preview, and resume-command tools but omits branching and conversion. To enable handoffs, a user must intentionally configure `showagent mcp` without `--read-only`; this adds only new sessions, never modifying or deleting originals. Never add `--allow-secrets`.

This skill cannot make a remote marketplace agent access a user's local stdio process. A listing can only perform handoffs where the agent environment has a working connection to that user's local showagent MCP server. Test this boundary in each target agent before promising that marketplace users can use local sessions.
