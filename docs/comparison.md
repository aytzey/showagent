# Local coding-agent session tools

This November 2026 snapshot summarizes how each project's README described
its session workflow at the time. Follow the project links for current
features and supported agents.
[Original snapshot](https://github.com/aytzey/showagent/blob/42612a4699eb5ac4338dde9f0669d8d312cd52ae/README.md#how-it-compares)

| Tool | Focus | How it handles local sessions |
|---|---|---|
| showagent | Terminal picker, CLI, and MCP for existing local sessions | Searches by workspace, branches at a user message, or previews and writes a native session for a different agent. [showagent](../README.md) |
| continues | Node.js picker and CLI | Generates structured context and injects it into a target tool. [continues](https://github.com/yigitkonur/cli-continues) |
| casr | Rust CLI for cross-agent resume | Converts through a common session model and verifies the result by reading it back. [casr](https://github.com/Dicklesworthstone/cross_agent_session_resumer) |
| hstry | Shared history database and search | Converts sessions into the target agent's native format. [hstry](https://github.com/byteowlz/hstry) |
| claude-squad and ccmanager | Parallel live agent sessions in terminals or worktrees | Organize active coding tasks. [claude-squad](https://github.com/smtg-ai/claude-squad) · [ccmanager](https://github.com/kbwo/ccmanager) |
| Agent Sessions | macOS interface for browsing agent history | Browse and search stored conversations. [Agent Sessions](https://github.com/jazzyalex/agent-sessions) |
