# showagent-session-handoff

A cross-agent `SKILL.md` package that teaches coding agents to search, preview, resume, and (only after explicit confirmation) branch or convert sessions through showagent's local MCP server.

## Requirements

- showagent installed locally and available on `PATH`
- The current coding agent connected to the local showagent MCP server
- For handoffs, the server is configured without `--read-only`; read-only search and resume work with that flag

This package does not install showagent, access session stores directly, or make a remote service capable of reaching local sessions. Transcript content returned through MCP enters the current agent's context. Common secret-like strings are redacted by default, but users should review data-sharing expectations for their model provider.

## Install

Use your agent's documented skill directory and install this folder as `showagent-session-handoff/`. The open Agent Skills format is supported by a growing set of coding agents; MCP server setup is separate and varies by client. See [tool-reference.md](references/tool-reference.md).

## Package for Agensi

From this directory, create a ZIP with one top-level folder:

```sh
zip -r showagent-session-handoff.zip showagent-session-handoff -x "*/.DS_Store" "*/Thumbs.db"
```

The archive should contain `showagent-session-handoff/SKILL.md` and the package's README and reference file. Run the repository package test before uploading.

## License

The package text is released under the repository's MIT license. The showagent name and marks remain subject to their applicable rights.
