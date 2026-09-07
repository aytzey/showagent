"""Guard against false evidence and accidental access to real provider stores."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("handoff", Path(__file__).with_name("verify-handoff.py"))
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


class EvidenceTests(unittest.TestCase):
    def test_personal_paths_credentials_and_executables_are_not_inherited(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            with patch.dict(os.environ, {"OPENAI_API_KEY": "never-copy", "PATH": "foreign-bin",
                                         "PI_CODING_AGENT_SESSION_DIR": "foreign-pi"}):
                env = handoff.isolated_env(root)
            self.assertNotIn("OPENAI_API_KEY", env)
            for name in ("HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "CODEX_HOME",
                         "CLAUDE_CONFIG_DIR", "GEMINI_CLI_HOME", "OPENCODE_DATA_HOME",
                         "JCODE_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "PATH"):
                self.assertTrue(Path(env[name]).is_relative_to(root), name)
            self.assertEqual(list(Path(env["PATH"]).iterdir()), [])

    def test_metadata_only_native_thread_is_not_a_pass(self):
        for result in ({"thread": {"id": "expected", "turns": [], "preview": "secret-marker"}},
                       {"thread": {"id": "wrong", "turns": ["secret-marker"]}},
                       {"thread": {"id": "expected", "turns": ["other conversation"]}}):
            with self.subTest(result=result), self.assertRaises(AssertionError):
                handoff.check_native_thread(result, "expected", "secret-marker")

    def test_native_history_must_contain_the_earlier_decision(self):
        result = {"thread": {"id": "expected", "turns": [{"items": ["secret-marker"]}]}}
        self.assertEqual(handoff.check_native_thread(result, "expected", "secret-marker"), 1)


if __name__ == "__main__":
    unittest.main()
