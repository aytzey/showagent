# showagent MCP tool reference

The MCP server runs locally over stdio. It has no hosted account, telemetry, or showagent upload path. The connected coding agent may send transcript text returned by `get_transcript` to its own model provider. Common secret-like values are redacted by default; this cannot guarantee that all personal, proprietary, or secret data is removed.

## Tools

| Tool | Input | Effect |
|---|---|---|
| `list_sessions` | Optional `provider`, `workspace`, `query`, `limit` | Read-only metadata and first/last user-message previews; newest first. Default limit 25, maximum 100. |
| `get_transcript` | Required exact `id`; optional `max_turns` | Reads recent user/assistant turns. Default 50, maximum 500. Secret-like values are redacted unless the server was explicitly started with `--allow-secrets`; do not enable that option. |
| `resume_command` | Required exact `id` | Returns a command and working directory. It does not execute the command. |
| `branch_session` | Required exact `id` | Writes a new native session for the same provider. It does not change the source. Requires the default writable server and explicit user confirmation. |
| `convert_session` | Required exact `id`, `target` | Writes a new native session for a different provider. It does not change the source. Requires the default writable server and explicit user confirmation. |

Valid provider IDs include `codex`, `claude`, `gemini`, `antigravity`, `opencode`, `mimocode`, `jcode`, and `pi`. `antigravity` sessions can be converted from, but not into. Conversion to OpenCode or MiMoCode requires that provider's CLI because showagent uses the CLI to import.

Antigravity cannot receive a new session, so `branch_session` is unsupported for every Antigravity session. Antigravity IDE sessions also cannot be natively resumed; the read-only `resume_command` tool returns an error for those sessions. A successful resume command is required before offering native resume.

`get_transcript` reports `total_turns`, `truncated`, and `secrets_redacted`. Before reading, verify the live tool description says the server redacts secret-like values; a server started with `--allow-secrets` instead advertises verbatim output. Do not rely on the response flag to prevent an unsafe read.

There is no delete tool. Branch and conversion preserve the original and create a separate session. They carry transferable user/assistant turns, not provider-private tool calls, approvals, encrypted reasoning, or attachments. MCP handoff has no dry-run tool or turn-scope parameter: `max_turns` only limits the read preview, while branch/convert copy all transferable turns. Before confirmation, show `total_turns`, the number previewed, and whether `truncated` is true. Explain when older, unpreviewed text will enter the new session. A request to copy only the previewed subset cannot be fulfilled by these MCP tools.

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

The read-only mode exposes search, transcript, and resume-command tools but omits branching and conversion. To enable handoffs, a user must intentionally configure `showagent mcp` without `--read-only`; this adds only new sessions, never modifying or deleting originals. Never add `--allow-secrets`.

This skill cannot make a remote marketplace agent access a user's local stdio process. A listing can only perform handoffs where the agent environment has a working connection to that user's local showagent MCP server. Test this boundary in each target agent before promising that marketplace users can use local sessions.
