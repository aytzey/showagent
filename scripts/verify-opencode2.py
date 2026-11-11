#!/usr/bin/env python3
"""Round-trip fabricated messages through a real OpenCode 2.x CLI, without a model."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--opencode", default="opencode")
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    opencode = shutil.which(args.opencode)
    if not opencode:
        parser.error("OpenCode CLI not found")
    opencode = str(Path(opencode).resolve())

    with tempfile.TemporaryDirectory(prefix="showagent-opencode2-") as root:
        home = Path(root)
        workspace = home / "workspace"
        workspace.mkdir()
        env = os.environ.copy()
        for key in list(env):
            if key.startswith("OPENCODE_"):
                del env[key]
        for key, path in {
            "HOME": ".", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data",
            "XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache",
            "CODEX_HOME": "codex", "CLAUDE_HOME": "claude", "JCODE_HOME": "jcode",
            "OPENCODE_DATA_HOME": "data/opencode", "MIMOCODE_HOME": "mimo",
            "GEMINI_CLI_HOME": "gemini", "ANTIGRAVITY_HOME": "antigravity",
            "PI_CODING_AGENT_DIR": "pi",
        }.items():
            env[key] = str(home / path)
        env.pop("PI_CODING_AGENT_SESSION_DIR", None)
        env["SHOWAGENT_NO_UPDATE_CHECK"] = "1"
        env["PATH"] = str(Path(opencode).parent) + os.pathsep + os.defpath

        def run(*command):
            result = subprocess.run(command, env=env, cwd=workspace, capture_output=True,
                                    text=True, timeout=90, check=True)
            return result.stdout

        version = run(opencode, "--version").strip()
        if not re.fullmatch(r"(?:opencode\s+)?v?2\.\d+\.\d+(?:[-+][\w.+-]+)?", version):
            parser.error("This check requires OpenCode 2.x")
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        run(opencode, "service", "set", "port", str(port))
        source_id = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
        turns = [("user", "Hello 世界 from isolated fixture"),
                 ("assistant", "Answer\n  indented"), ("user", "A second question")]
        source = home / "codex/sessions/fixture.jsonl"
        source.parent.mkdir(parents=True)
        records = [{"timestamp": "2026-11-11T09:00:00Z", "type": "session_meta",
                    "payload": {"id": source_id, "cwd": str(workspace)}}]
        for index, (role, text) in enumerate(turns, 1):
            records.append({"timestamp": f"2026-11-11T09:0{index}:00Z", "type": "response_item",
                            "payload": {"type": "message", "role": role,
                                        "content": [{"type": "input_text" if role == "user" else "output_text",
                                                     "text": text}]}})
        source.write_text("".join(json.dumps(row) + "\n" for row in records), encoding="utf-8")
        original = source.read_bytes()
        try:
            output = run(binary, "convert", source_id, "--to", "opencode")
            match = re.search(r"ses_[A-Za-z0-9]+", output)
            assert match, "conversion did not report a session ID"
            session_id = match.group(0)
            exported = json.loads(run(opencode, "session", "export", session_id))
            actual = [(message["type"], message["text"] if message["type"] == "user"
                       else "\n".join(part["text"] for part in message["content"] if part["type"] == "text"))
                      for message in exported["messages"]]
            assert actual == turns, "native import/export changed conversation text"
            rows = json.loads(run(binary, "list", "--json"))
            assert len([row for row in rows if row["provider"] == "opencode"]) == 1
            transcript = json.loads(run(binary, "transcript", session_id, "--json"))
            assert [(turn["role"], turn["text"]) for turn in transcript["turns"]] == turns
            assert source.read_bytes() == original, "conversion changed the source"
            run(opencode, "session", "delete", session_id)
            assert json.loads(run(opencode, "session", "list", "--format", "json")) == []
            print(f"{version}: native import/export/discovery/transcript/delete passed; no model call")
        finally:
            subprocess.run([opencode, "service", "stop"], env=env, cwd=workspace,
                           capture_output=True, timeout=30, check=True)


if __name__ == "__main__":
    main()
