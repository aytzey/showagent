# Security policy

## Supported versions

Security fixes are released on the latest tagged version. Upgrade to the most
recent release before reporting behavior that may already be fixed.

## Reporting a vulnerability

Use GitHub's private **Security > Report a vulnerability** flow for this
repository. Do not open a public issue for vulnerabilities, leaked session
content, credentials, or terminal-escape payloads.

Include the affected version, operating system, reproduction steps, expected
impact, and whether the report involves a particular agent's session format.
Please use fabricated transcripts and redacted paths whenever possible.

You should receive an acknowledgement within 72 hours. A fix and disclosure
timeline will be coordinated privately according to severity.

## Security boundaries

showagent reads sensitive local conversation history. Message previews and MCP
transcripts redact common secret-like values by default, but no detector is
complete. MCP clients may send tool output to their configured model provider.
Use `showagent mcp --read-only` to remove tools that write session copies, and
review a client's own approval and data-retention settings before granting it
access.
