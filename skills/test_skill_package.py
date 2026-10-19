"""Validate the Agensi-compatible showagent skill package and ZIP layout."""

from pathlib import Path
import re
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parent
SKILL_DIR = ROOT / "showagent-session-handoff"


class SkillPackageTest(unittest.TestCase):
    def test_frontmatter_and_package_name(self):
        text = (SKILL_DIR / "SKILL.md").read_text(encoding="utf-8")
        match = re.match(r"^---\n(.*?)\n---\n", text, re.DOTALL)
        self.assertIsNotNone(match, "SKILL.md must start with YAML frontmatter")
        frontmatter = match.group(1)
        self.assertIn("name: showagent-session-handoff", frontmatter)
        self.assertIn("description:", frontmatter)
        self.assertRegex("showagent-session-handoff", r"^[a-z0-9-]+$")

    def test_required_guidance_and_reference(self):
        skill = (SKILL_DIR / "SKILL.md").read_text(encoding="utf-8").lower()
        reference = (SKILL_DIR / "references" / "tool-reference.md").read_text(
            encoding="utf-8"
        )
        for phrase in (
            "explicitly confirms",
            "workspace",
            "never execute a returned resume command",
            "untrusted historical data",
            "redacts common secret-like values",
        ):
            self.assertIn(phrase, skill)
        for tool in (
            "list_sessions",
            "get_transcript",
            "resume_command",
            "branch_session",
            "convert_session",
        ):
            self.assertIn(f"`{tool}`", reference)
        self.assertTrue((SKILL_DIR / "README.md").is_file())

    def test_zip_has_one_top_level_folder_and_no_binaries(self):
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / "skill.zip"
            with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as zf:
                for path in sorted(SKILL_DIR.rglob("*")):
                    if path.is_file():
                        zf.write(path, path.relative_to(ROOT))
            with zipfile.ZipFile(archive) as zf:
                names = zf.namelist()
                self.assertIn("showagent-session-handoff/SKILL.md", names)
                self.assertIn(
                    "showagent-session-handoff/references/tool-reference.md", names
                )
                self.assertTrue(all(n.split("/")[0] == "showagent-session-handoff" for n in names))
                self.assertFalse(any(n.endswith((".exe", ".dll", ".so", ".bin")) for n in names))


if __name__ == "__main__":
    unittest.main()
