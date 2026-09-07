"""Fixture and launcher regression checks; never use real agent stores."""

import json
import hashlib
import os
from pathlib import Path
import queue
import re
import shutil
import subprocess
import tempfile
import threading
import unittest


DEMO = Path(__file__).resolve().parent
RUNS = DEMO / ".runs"
BASH = os.environ.get("BASH_BIN") or shutil.which("bash")


def native_fs(path):
    # Converted Claude project slugs repeat the cwd and can exceed MAX_PATH.
    return Path("\\\\?\\" + str(path.resolve())) if os.name == "nt" else path


class DemoTests(unittest.TestCase):
    def setUp(self):
        if not BASH:
            self.skipTest("bash is required")
        RUNS.mkdir(exist_ok=True)
        self.root = Path(tempfile.mkdtemp(prefix="test-", dir=RUNS)).resolve()
        self.addCleanup(self.remove_owned_root)

    def remove_owned_root(self):
        self.remove_test_tree(self.root)

    def remove_test_tree(self, root):
        if not root.resolve().is_relative_to(RUNS.resolve()):
            raise AssertionError("test cleanup escaped demo/.runs")
        shutil.rmtree(native_fs(root))

    def binary(self):
        candidate = os.environ.get("SHOWAGENT_DEMO_BINARY")
        if not candidate:
            self.skipTest("set SHOWAGENT_DEMO_BINARY to run launcher smoke checks")
        return str(Path(candidate).resolve())

    def launcher_environment(self):
        foreign = self.root / "foreign"
        foreign.mkdir(exist_ok=True)
        pi_root = foreign / "pi-sessions"
        pi_root.mkdir(exist_ok=True)
        sentinel = pi_root / "foreign.jsonl"
        sentinel.write_text(
            json.dumps({"type": "session", "version": 3, "id": "foreign-pi-sentinel",
                        "timestamp": "2026-09-06T12:00:00Z", "cwd": str(foreign)}) + "\n" +
            json.dumps({"type": "message", "id": "00000001", "parentId": None,
                        "timestamp": "2026-09-06T12:01:00Z",
                        "message": {"role": "user", "content": "FOREIGN_PI_SENTINEL"}}) + "\n",
            encoding="utf-8",
        )
        self.sentinel = sentinel
        fake_bin = foreign / "bin"
        fake_bin.mkdir(exist_ok=True)
        self.real_cli_marker = foreign / "unexpected-real-cli-call"
        for name in ("pi", "opencode", "jcode", "claude", "codex"):
            stub = fake_bin / name
            stub.write_text("#!/bin/sh\nprintf unexpected > '" + self.real_cli_marker.as_posix() + "'\n", encoding="utf-8")
            stub.chmod(0o755)
        return {
            **os.environ,
            "SHOWAGENT_DEMO_BINARY": self.binary(),
            "NOW": "2026-09-06T12:00:00Z",
            "HOME": str(foreign), "USERPROFILE": str(foreign),
            "PI_CODING_AGENT_DIR": str(foreign),
            "PI_CODING_AGENT_SESSION_DIR": str(pi_root),
            "PATH": str(fake_bin) + os.pathsep + os.environ.get("PATH", ""),
        }

    def generate(self, destination):
        return subprocess.run(
            [BASH, (DEMO / "fixtures" / "gen.sh").as_posix(), destination.as_posix()],
            text=True, encoding="utf-8", capture_output=True,
            env={**os.environ, "NOW": "2026-09-06T12:00:00Z"},
            check=False,
        )

    def test_generator_refuses_existing_directory_without_modifying_it(self):
        destination = self.root / "existing"
        source = destination / ".codex" / "sessions" / "do-not-touch.jsonl"
        source.parent.mkdir(parents=True)
        source.write_text("synthetic original sentinel", encoding="utf-8")
        before = source.read_bytes()

        result = self.generate(destination)

        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertEqual(source.read_bytes(), before)
        self.assertEqual(list(destination.rglob("*")), [
            destination / ".codex", destination / ".codex" / "sessions", source,
        ])

    def test_generator_returns_owned_root_and_writes_valid_json_paths(self):
        name = "sample café 'quoted'" if os.name == "nt" else 'sample café "quoted" \\path'
        destination = self.root / name

        result = self.generate(destination)

        self.assertEqual(result.returncode, 0, result.stderr)
        reported = Path(result.stdout.strip())
        self.assertEqual(reported.resolve(), destination.resolve())
        self.assertEqual((destination / ".showagent-demo-owned").read_text().strip(), "showagent-demo-v1")
        files = list(destination.rglob("*.jsonl")) + list(destination.rglob("*.json"))
        self.assertEqual(len(files), 12)
        saw_quoted_message = False
        for path in files:
            for line in path.read_text(encoding="utf-8").splitlines():
                record = json.loads(line)
                cwd = record.get("cwd") or record.get("payload", {}).get("cwd")
                if cwd:
                    self.assertTrue(Path(cwd).resolve().is_relative_to(destination.resolve()), cwd)
                saw_quoted_message |= '"Retry-After"' in line.replace('\\"', '"')
        self.assertTrue(saw_quoted_message)

    def test_launcher_excludes_foreign_pi_sessions_and_real_cli_path(self):
        result = subprocess.run(
            [BASH, (DEMO / "run-isolated.sh").as_posix(), "list", "--json"],
            env=self.launcher_environment(), capture_output=True,
            text=True, encoding="utf-8", timeout=45, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("FOREIGN_PI_SENTINEL", result.stdout)
        self.assertFalse(self.real_cli_marker.exists())
        self.assertIn("FOREIGN_PI_SENTINEL", self.sentinel.read_text(encoding="utf-8"))
        fixture_line = next(line for line in result.stderr.splitlines() if line.startswith("Demo fixtures: "))
        root = Path(fixture_line.removeprefix("Demo fixtures: ")).resolve()
        self.addCleanup(self.remove_test_tree, root)
        self.assertTrue(root.is_relative_to(RUNS.resolve()))
        rows = json.loads(result.stdout)
        self.assertEqual(len(rows), 12)
        self.assertEqual({row["provider"] for row in rows}, {"codex", "claude", "gemini"})
        for row in rows:
            self.assertTrue(Path(row["workspace"]).resolve().is_relative_to(root))

    def test_recording_startup_failure_never_returns_to_personal_shell(self):
        foreign_bin = self.root / "foreign-bin"
        foreign_bin.mkdir()
        sentinel = self.root / "personal-showagent-called"
        command = foreign_bin / "showagent"
        command.write_text('#!/bin/sh\nprintf called > "$DEMO_SENTINEL_LOG"\n', encoding="utf-8")
        command.chmod(0o755)
        tape = (DEMO / "demo.tape").read_text(encoding="utf-8")
        startup = re.search(r'^Type "([^"]*run-isolated.sh[^"]*)"$', tape, re.MULTILINE)
        self.assertIsNotNone(startup, "recording startup command is missing")
        result = subprocess.run(
            [BASH, "--noprofile", "--norc"],
            input=startup.group(1) + "\nshowagent\n",
            cwd=DEMO.parent,
            env={**os.environ, "SHOWAGENT_DEMO_BINARY": str(self.root / "missing-binary"),
                 "DEMO_SENTINEL_LOG": str(sentinel),
                 "PATH": str(foreign_bin) + os.pathsep + os.environ.get("PATH", "")},
            capture_output=True, text=True, encoding="utf-8", timeout=30, check=False,
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("build demo/.build/showagent first", result.stderr)
        self.assertFalse(sentinel.exists(), "failed recording escaped into the personal shell")

    def test_preview_and_conversion_preserve_sources_in_isolated_shell(self):
        stderr_file = self.root / "shell-stderr.txt"
        with stderr_file.open("w", encoding="utf-8") as stderr:
            process = subprocess.Popen(
                [BASH, (DEMO / "run-isolated.sh").as_posix(), "--shell"],
                env=self.launcher_environment(), stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=stderr,
                text=True, encoding="utf-8", bufsize=1,
            )
            lines = queue.Queue()

            def collect():
                for line in process.stdout:
                    lines.put(line.rstrip("\r\n"))

            threading.Thread(target=collect, daemon=True).start()

            def send(command):
                process.stdin.write(command + "\n")
                process.stdin.flush()

            def until(marker):
                received = []
                while True:
                    try:
                        line = lines.get(timeout=45)
                    except queue.Empty:
                        self.fail("isolated shell did not reach " + marker)
                    if line.startswith(marker):
                        return received, line[len(marker):]
                    received.append(line)

            def snapshot(root):
                scan_root = native_fs(root)
                return {p.relative_to(scan_root): hashlib.sha256(p.read_bytes()).hexdigest()
                        for p in scan_root.rglob("*") if p.is_file() and p.suffix in (".json", ".jsonl")}

            try:
                send("printf '__ROOT__:%s\\n' \"$USERPROFILE\"")
                _, root_string = until("__ROOT__:")
                root = Path(root_string).resolve()
                self.addCleanup(self.remove_test_tree, root)
                self.assertTrue(root.is_relative_to(RUNS.resolve()))
                before = snapshot(root)
                self.assertEqual(len(before), 12)
                send("printf '__PATH__:%s\\n' \"$PATH\"")
                _, child_path = until("__PATH__:")
                self.assertNotIn("foreign", child_path)
                self.assertTrue(child_path.replace("\\", "/").endswith("/demo/bin"), child_path)
                source_id = "1f7c9a2e-4b31-4c8e-9d02-8a5e3f6b1c44"
                send("showagent convert " + source_id + " --to claude --dry-run")
                send("printf '__PREVIEW__:%s\\n' \"$?\"")
                preview, code = until("__PREVIEW__:")
                self.assertEqual(code, "0", preview)
                self.assertIn("5 transferable turns", "\n".join(preview))
                self.assertEqual(snapshot(root), before)
                send("showagent convert " + source_id + " --to claude")
                send("printf '__CONVERT__:%s\\n' \"$?\"")
                converted, code = until("__CONVERT__:")
                self.assertEqual(code, "0", converted)
                after = snapshot(root)
                self.assertEqual(len(after), len(before) + 1)
                for path, digest in before.items():
                    self.assertEqual(after[path], digest)
                self.assertFalse(self.real_cli_marker.exists())
                send("exit")
                self.assertEqual(process.wait(timeout=10), 0)
            finally:
                if process.poll() is None:
                    # Let the isolated child shell exit before its launcher,
                    # including after a failed assertion on Windows.
                    try:
                        send("exit")
                        process.stdin.close()
                        process.wait(timeout=10)
                    except (BrokenPipeError, subprocess.TimeoutExpired):
                        process.kill()
                        process.wait(timeout=10)
                if not process.stdin.closed:
                    process.stdin.close()
                process.stdout.close()


if __name__ == "__main__":
    unittest.main()
