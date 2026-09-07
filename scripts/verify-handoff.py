#!/usr/bin/env python3
"""Exercise a built binary with disposable conversations, never the user's stores.

Optional --codex checks the native local thread reader, without a model call.
File conversion, native loading, and model continuation are separate evidence.
"""

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import queue
import subprocess
import tempfile
import threading
import time
import uuid


def isolated_env(root):
    """Allow OS essentials only; do not inherit tokens, config, PATH or stores."""
    env = {key: os.environ[key] for key in ("SystemRoot", "WINDIR", "COMSPEC", "PATHEXT") if key in os.environ}
    locations = {
        "HOME": "home", "USERPROFILE": "home", "APPDATA": "home/appdata",
        "LOCALAPPDATA": "home/localappdata", "XDG_CONFIG_HOME": "home/config",
        "XDG_DATA_HOME": "home/data", "XDG_CACHE_HOME": "home/cache",
        "CODEX_HOME": "codex", "CLAUDE_HOME": "claude",
        "CLAUDE_CONFIG_DIR": "claude", "GEMINI_CLI_HOME": "gemini",
        "OPENCODE_DATA_HOME": "opencode", "JCODE_HOME": "jcode",
        "PI_CODING_AGENT_DIR": "pi", "PI_CODING_AGENT_SESSION_DIR": "pi/sessions",
        "SHOWAGENT_LEARNINGS_DIR": "learnings", "PATH": "empty-bin",
        "TEMP": "tmp", "TMP": "tmp", "TMPDIR": "tmp",
    }
    for name, relative in locations.items():
        directory = root / relative
        directory.mkdir(parents=True, exist_ok=True)
        env[name] = str(directory)
    env.update(SHOWAGENT_NO_UPDATE_CHECK="1", NO_COLOR="1", TERM="dumb")
    return env


def files(root):
    return {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in root.rglob("*") if p.is_file()}


def seed(root, workspace):
    session_id = str(uuid.uuid4())
    marker = "decision-" + uuid.uuid4().hex
    turns = [
        ("user", "Fix the flaky TTL test. password=synthetic-fixture-only"),
        ("assistant", "Use a fake clock.\n```go\n  clock.Advance(time.Minute)\n```"),
        ("user", "The previously chosen decision code is " + marker),
        ("assistant", "Keep Unicode: Türkçe — and indentation. Do not use sleeps."),
        ("user", "Continue using the earlier decision."),
    ]
    timestamp = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    records = [{"timestamp": timestamp, "type": "session_meta",
                "payload": {"id": session_id, "cwd": str(workspace)}}]
    for role, text in turns:
        records.append({"timestamp": timestamp, "type": "response_item", "payload": {
            "type": "message", "role": role,
            "content": [{"type": "input_text" if role == "user" else "output_text", "text": text}]}})
    records.append({"timestamp": timestamp, "type": "response_item", "payload": {
        "type": "function_call", "name": "read_file", "arguments": "private-tool-state-fixture"}})
    source = root / "codex" / "sessions" / ("rollout-" + session_id + ".jsonl")
    source.parent.mkdir(parents=True, exist_ok=True)
    source.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n" for r in records), encoding="utf-8")
    return session_id, source, marker


def command(binary, args, env, workspace, timeout=30):
    result = subprocess.run([str(binary), *args], env=env, cwd=workspace,
                            capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=timeout)
    if result.returncode:
        # Only synthetic data is available to the process. Bound failure output.
        raise RuntimeError(f"{' '.join(args)} exited {result.returncode}: {result.stderr[-1000:]}")
    return result.stdout


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def check_native_thread(result, session_id, marker):
    thread = result.get("thread", {})
    require(thread.get("id") == session_id, "Native reader returned a different thread")
    require(marker in json.dumps(thread.get("turns", [])),
            "Native reader did not return the earlier decision in its visible history")
    return len(thread["turns"])


def native_codex_read(binary, env, workspace, session_id, marker):
    version = command(binary, ["--version"], env, workspace).strip()
    messages = queue.Queue()
    process = subprocess.Popen([str(binary), "app-server", "--stdio"], env=env, cwd=workspace,
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               text=True, encoding="utf-8", errors="replace")
    def receive():
        for line in process.stdout:
            try:
                messages.put(json.loads(line))
            except json.JSONDecodeError:
                continue
        messages.put(None)
    reader = threading.Thread(target=receive, daemon=True)
    reader.start()
    def request(identifier, method, params):
        process.stdin.write(json.dumps({"id": identifier, "method": method, "params": params}) + "\n")
        process.stdin.flush()
        deadline = time.monotonic() + 30
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"Codex {method} timed out")
            message = messages.get(timeout=remaining)
            if message is None:
                raise RuntimeError("Codex app-server stopped before replying")
            if message.get("id") != identifier:
                continue
            if "error" in message:
                raise RuntimeError(f"Codex {method}: {message['error']}")
            return message["result"]
    try:
        request(1, "initialize", {"clientInfo": {"name": "showagent-compatibility", "version": "1"}})
        process.stdin.write(json.dumps({"method": "initialized"}) + "\n")
        process.stdin.flush()
        result = request(2, "thread/read", {"threadId": session_id, "includeTurns": True})
        turn_count = check_native_thread(result, session_id, marker)
        return {"status": "passed", "version": version, "method": "app-server thread/read includeTurns",
                "direction": "Claude-format fixture -> Codex", "visible_turns": turn_count, "model_call": False}
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        reader.join(timeout=2)
        process.stdin.close()
        process.stdout.close()


def verify(binary, report, codex=None):
    # Antivirus/file indexing can briefly hold a Windows fixture after exit.
    # A best-effort temp cleanup must not hide the actual validation failure.
    with tempfile.TemporaryDirectory(prefix="showagent-proof-", ignore_cleanup_errors=True) as temporary:
        root = Path(temporary).resolve()
        env = isolated_env(root)
        workspace = root / "workspaces" / "Türkçe project with space's"
        workspace.mkdir(parents=True)
        invoke = lambda *args: command(binary, args, env, workspace)
        def passed(name):
            report["checks"].append({"name": name, "status": "passed"})
        report["binary_version"] = invoke("--version").strip()
        report["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        help_text = invoke("--help")
        require("transcript" in help_text and "--dry-run" in help_text, "Help lacks advertised commands")
        require(json.loads(invoke("list", "--json")) == [], "External sessions leaked into isolated environment")
        passed("empty discovery and advertised help")
        session_id, source, marker = seed(root, workspace)
        original_hash = hashlib.sha256(source.read_bytes()).hexdigest()
        rows = json.loads(invoke("list", "--json"))
        require(len(rows) == 1 and rows[0]["id"] == session_id, "Fixture was not uniquely discovered")
        require("synthetic-fixture-only" not in rows[0]["first_message"], "Password leaked into preview")
        transcript = json.loads(invoke("transcript", session_id, "--json"))
        expected = transcript["turns"]
        require(len(expected) == 5 and transcript["secrets_redacted"], "Unexpected source transcript")
        require("synthetic-fixture-only" not in json.dumps(expected), "Password leaked into transcript")
        require("untrusted" in transcript["warning"].lower(), "Historical-data boundary missing")
        passed("fixture discovery, secret redaction and history warning")
        before = files(root)
        preview = invoke("convert", session_id, "--to", "claude", "--dry-run")
        require("5" in preview and "tool" in preview.lower(), "Handoff preview lacks scope/loss information")
        require(files(root) == before, "Dry-run changed the filesystem")
        passed("dry-run leaves all fixture files unchanged")
        invoke("convert", session_id, "--to", "claude")
        rows = json.loads(invoke("list", "--json"))
        claude = [r for r in rows if r["provider"] == "claude"]
        require(len(claude) == 1, "Converted Claude conversation not discoverable")
        claude_id = claude[0]["id"]
        require(json.loads(invoke("transcript", claude_id, "--json"))["turns"] == expected, "Claude turn fidelity changed")
        require("claude" in invoke("info", claude_id).lower(), "Claude resume recipe missing")
        passed("Codex -> Claude preserves text, Unicode, code and resume recipe")
        invoke("convert", claude_id, "--to", "codex")
        rows = json.loads(invoke("list", "--json"))
        returned = [r for r in rows if r["provider"] == "codex" and r["id"] != session_id]
        require(len(returned) == 1, "Return-trip Codex conversation not discoverable")
        returned_id = returned[0]["id"]
        require(json.loads(invoke("transcript", returned_id, "--json"))["turns"] == expected, "Round-trip fidelity changed")
        passed("Claude -> Codex return trip preserves transferable conversation")
        invoke("convert", session_id, "--to", "pi", "--scope", "last:2")
        pi = [r for r in json.loads(invoke("list", "--json")) if r["provider"] == "pi"]
        require(len(pi) == 1, "Scoped Pi copy not discoverable")
        require(json.loads(invoke("transcript", pi[0]["id"], "--json"))["turns"] == expected[-2:], "Scope trimming mismatch")
        bounded = json.loads(invoke("transcript", returned_id, "--max-turns", "2", "--json"))
        require(bounded["truncated"] and bounded["turns"] == expected[-2:], "Transcript bound mismatch")
        require(hashlib.sha256(source.read_bytes()).hexdigest() == original_hash, "Original fixture was changed")
        passed("scoped copy, bounded transcript and original preservation")
        report["synthetic_conversion"] = "passed"
        if codex:
            report["native_loader"] = {"status": "failed", "reason": "Native reader validation did not complete"}
            report["native_loader"] = native_codex_read(codex, env, workspace, returned_id, marker)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--codex", type=Path, help="Optional real Codex executable; local reader only, no model calls")
    args = parser.parse_args()
    report = {"date_utc": dt.datetime.now(dt.timezone.utc).isoformat(), "platform": platform.platform(),
              "checks": [], "synthetic_conversion": "not_run", "native_loader": {"status": "not_run"},
              "model_continuation": {"status": "not_run", "reason": "This harness does not call a model."}}
    try:
        verify(args.binary.resolve(strict=True), report, args.codex.resolve(strict=True) if args.codex else None)
        report["status"] = "passed"
    except (AssertionError, OSError, RuntimeError, ValueError, KeyError, subprocess.SubprocessError, queue.Empty) as error:
        report["status"] = "failed"
        report["error"] = str(error) or type(error).__name__
    content = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(content, encoding="utf-8")
    print(content)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
