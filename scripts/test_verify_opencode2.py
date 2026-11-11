import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("verify-opencode2.py").resolve()


class OpenCodeSmokeTest(unittest.TestCase):
    def test_relative_opencode_path_runs_from_the_isolated_workspace(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            opencode = root / "opencode"
            opencode.write_text('#!/bin/sh\necho "opencode v1.17.15"\n', encoding="utf-8")
            opencode.chmod(0o755)
            result = subprocess.run(
                [sys.executable, str(SCRIPT), "--binary", "./showagent", "--opencode", "./opencode"],
                cwd=root, env={**os.environ, "PYTHONUTF8": "1"}, capture_output=True, text=True, timeout=10,
            )
            self.assertEqual(result.returncode, 2, result.stderr)
            self.assertIn("This check requires OpenCode 2.x", result.stderr)
            self.assertNotIn("FileNotFoundError", result.stderr)


if __name__ == "__main__":
    unittest.main()
