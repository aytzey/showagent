#!/usr/bin/env python3
"""Opt-in DevPass launcher; plain claude keeps its existing auth and settings."""

import os
from pathlib import Path
import sys

MODEL = "mimo-v2.6-pro"


def main():
    args = sys.argv[1:]
    boundary = args.index("--") if "--" in args else len(args)
    mimo = "--mimo" in args[:boundary]
    if mimo:
        effort = "high"
        for index, arg in enumerate(args[:boundary]):
            if arg == "--effort" or arg.startswith("--effort="):
                effort = arg.partition("=")[2] if "=" in arg else (args[index + 1] if index + 1 < boundary else "")
                if effort not in ("low", "medium", "high"):
                    raise ValueError("MiMo supports --effort low, medium, or high")
        if not any(arg.split("=", 1)[0] == "--effort" for arg in args[:boundary]):
            args = ["--effort", effort, *args]
            boundary += 2
        if any(arg.split("=", 1)[0] in ("--model", "--fallback-model")
               for arg in args[:boundary]):
            raise ValueError("--mimo selects MiMo V2.6 Pro; omit --model/--fallback-model")
        args = [arg for arg in args[:boundary] if arg != "--mimo"] + args[boundary:]

    # Skip this wrapper in PATH. The optional symlink also covers GUI launchers
    # whose PATH does not contain the user's NVM installation.
    candidates = [Path(directory) / "claude" for directory in os.get_exec_path()]
    candidates.append(Path.home() / ".local/lib/claude-native")
    native = next((path for path in candidates
                   if path.is_file() and os.access(path, os.X_OK)
                   and path.resolve() != Path(__file__).resolve()), None)
    if native is None:
        raise ValueError("original Claude CLI not found in PATH or ~/.local/lib/claude-native")

    env = os.environ.copy()
    if mimo:
        key_path = Path.home() / ".config/claude-mimo/api-key"
        if key_path.stat().st_mode & 0o077:
            raise ValueError("DevPass key must be private: chmod 600 ~/.config/claude-mimo/api-key")
        key = key_path.read_text().strip()
        if not key or any(char.isspace() for char in key):
            raise ValueError("DevPass key is empty or invalid")
        for name in ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"):
            env.pop(name, None)
        env.update(ANTHROPIC_BASE_URL="https://api.llmgateway.io",
                   ANTHROPIC_AUTH_TOKEN=key,
                   ANTHROPIC_MODEL=MODEL,
                   ANTHROPIC_SMALL_FAST_MODEL=MODEL,
                   CLAUDE_CODE_SUBAGENT_MODEL=MODEL,
                   ANTHROPIC_CUSTOM_MODEL_OPTION=MODEL,
                   ANTHROPIC_CUSTOM_MODEL_OPTION_NAME="MiMo V2.6 Pro (DevPass)",
                   CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1")
        for alias in ("SONNET", "OPUS", "HAIKU", "FABLE"):
            env[f"ANTHROPIC_DEFAULT_{alias}_MODEL"] = MODEL
        for provider in ("BEDROCK", "VERTEX", "FOUNDRY"):
            env[f"CLAUDE_CODE_USE_{provider}"] = "0"
        args = ["--model", MODEL, *args]
    os.execve(native, [str(native), *args], env)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"claude: {error}", file=sys.stderr)
        sys.exit(1)
