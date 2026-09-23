"""Run with: python3 -B -m unittest discover -s scripts -p test_claude_mimo.py"""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

LAUNCHER = Path(__file__).with_name("claude-mimo.py")


class ClaudeMiMoTest(unittest.TestCase):
    def test_opt_in_credentials_arguments_and_default_isolation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            wrapper = root / "wrapper"
            native = root / "native"
            wrapper.mkdir()
            native.mkdir()
            (wrapper / "claude").symlink_to(LAUNCHER)
            stub = native / "claude"
            stub.write_text(f"#!{sys.executable}\n"
                            "import json, os, sys\n"
                            "print(json.dumps({'args': sys.argv[1:], 'env': dict(os.environ)}))\n")
            stub.chmod(0o755)
            env = {**os.environ, "HOME": str(root),
                   "PATH": f"{wrapper}:{native}:/usr/bin:/bin",
                   "ANTHROPIC_API_KEY": "existing-api-key",
                   "CLAUDE_CODE_OAUTH_TOKEN": "existing-subscription-token"}

            def run(*args):
                return subprocess.run([sys.executable, str(LAUNCHER), *args],
                                      env=env, text=True, capture_output=True, check=False)

            # Default auth works even with no DevPass key/configuration.
            plain = run("--resume", "session with spaces")
            self.assertEqual(plain.returncode, 0, plain.stderr)
            default = json.loads(plain.stdout)
            self.assertEqual(default["args"], ["--resume", "session with spaces"])
            self.assertEqual(default["env"]["ANTHROPIC_API_KEY"], "existing-api-key")
            self.assertEqual(run("--mimo", "--version").returncode, 1)
            literal = json.loads(run("--", "--mimo").stdout)
            self.assertEqual(literal["args"], ["--", "--mimo"])

            key = root / ".config/claude-mimo/api-key"
            key.parent.mkdir(parents=True, mode=0o700)
            key.write_text("test-devpass-key\n")
            key.chmod(0o600)
            result = run("--resume", "session with spaces", "--mimo", "--", "--mimo")
            self.assertEqual(result.returncode, 0, result.stderr)
            selected = json.loads(result.stdout)
            self.assertEqual(selected["args"], ["--model", "mimo-v2.6-pro", "--effort", "high", "--resume",
                                                "session with spaces", "--", "--mimo"])
            self.assertNotIn("test-devpass-key", selected["args"])
            selected_env = selected["env"]
            self.assertEqual(selected_env["ANTHROPIC_AUTH_TOKEN"], "test-devpass-key")
            self.assertEqual(selected_env["ANTHROPIC_BASE_URL"], "https://api.llmgateway.io")
            self.assertEqual(selected_env["CLAUDE_CODE_SUBAGENT_MODEL"], "mimo-v2.6-pro")
            self.assertNotIn("ANTHROPIC_API_KEY", selected_env)
            self.assertNotIn("CLAUDE_CODE_OAUTH_TOKEN", selected_env)
            self.assertEqual(json.loads(run("--resume", "session with spaces").stdout), default)
            for flag in ("--model=sonnet", "--fallback-model=opus"):
                self.assertEqual(run("--mimo", flag).returncode, 1)
            self.assertEqual(run("--mimo", "--effort", "xhigh").returncode, 1)
            low = json.loads(run("--mimo", "--effort=low").stdout)
            self.assertIn("--effort=low", low["args"])
            self.assertNotIn("high", low["args"])
            key.chmod(0o644)
            self.assertEqual(run("--mimo").returncode, 1)
            self.assertEqual(run("--version").returncode, 0)


if __name__ == "__main__":
    unittest.main()
