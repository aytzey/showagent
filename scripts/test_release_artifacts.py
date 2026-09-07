"""Release contract tests; fixtures contain no executable or user history."""

import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile


SCRIPT = Path(__file__).with_name("release_artifacts.py")
TARGETS = ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64")
TAG = "v1.2.3"


class ReleaseArtifactsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.dist = self.root / "dist"
        self.dist.mkdir()
        self.license = self.root / "LICENSE"
        self.license.write_text("MIT fixture\n", encoding="utf-8")
        self.readme = self.root / "README.md"
        self.readme.write_text("Fixture documentation\n", encoding="utf-8")
        self.template = self.root / "server.json"
        self.template.write_text(json.dumps({
            "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
            "name": "io.github.aytzey/showagent",
            "description": "Search and convert local coding sessions.",
            "repository": {"url": "https://github.com/aytzey/showagent", "source": "github"},
            "version": "0.11.0", "packages": [],
        }), encoding="utf-8")

    def run_script(self, *args, success=True):
        result = subprocess.run([sys.executable, str(SCRIPT), *map(str, args)],
                                capture_output=True, text=True, timeout=30)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def make_target(self, target):
        goos, goarch = target.split("_")
        entry = "showagent.exe" if goos == "windows" else "showagent"
        stem = f"showagent_{TAG}_{target}"
        binary = self.dist / (stem + (".exe" if goos == "windows" else ""))
        binary.write_bytes(b"fixture binary: " + target.encode())
        members = {entry: binary.read_bytes(), "LICENSE": self.license.read_bytes(),
                   "README.md": self.readme.read_bytes()}
        if goos == "windows":
            with zipfile.ZipFile(self.dist / f"{stem}.zip", "w") as archive:
                for name, data in members.items():
                    archive.writestr(name, data)
        else:
            with tarfile.open(self.dist / f"{stem}.tar.gz", "w:gz") as archive:
                for name, data in members.items():
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    info.mode = 0o755 if name == entry else 0o644
                    archive.addfile(info, io.BytesIO(data))
        self.run_script("pack", "--tag", TAG, "--goos", goos, "--goarch", goarch,
                        "--binary", binary, "--dist", self.dist,
                        "--license", self.license, "--readme", self.readme)
        return binary

    def make_release(self):
        for target in TARGETS:
            self.make_target(target)
        self.run_script("finalize", "--tag", TAG, "--dist", self.dist,
                        "--server-template", self.template)

    def test_bundle_native_command_platform_and_permissions(self):
        for target in ("linux_amd64", "windows_amd64"):
            with self.subTest(target=target):
                binary = self.make_target(target)
                path = self.dist / f"showagent_{TAG}_{target}.mcpb"
                with zipfile.ZipFile(path) as bundle:
                    manifest = json.loads(bundle.read("manifest.json"))
                    entry = manifest["server"]["entry_point"]
                    self.assertEqual(manifest["version"], "1.2.3")
                    self.assertEqual(manifest["manifest_version"], "0.3")
                    self.assertEqual(manifest["server"]["mcp_config"]["args"], ["mcp"])
                    self.assertEqual(manifest["server"]["mcp_config"]["command"], "${__dirname}/" + entry)
                    expected_platform = "win32" if target.startswith("windows") else "linux"
                    self.assertEqual(manifest["compatibility"]["platforms"], [expected_platform])
                    self.assertEqual(bundle.read(entry), binary.read_bytes())
                    self.assertEqual(bundle.getinfo(entry).external_attr >> 16 & 0o777, 0o755)

    def test_complete_release_uses_built_bytes_and_leaves_template_unchanged(self):
        original = self.template.read_bytes()
        self.make_release()
        self.assertEqual(self.template.read_bytes(), original)
        manifest = json.loads((self.dist / "server.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["version"], "1.2.3")
        self.assertEqual(len(manifest["packages"]), 5)
        for package in manifest["packages"]:
            name = package["identifier"].rsplit("/", 1)[1]
            self.assertEqual(package["identifier"], f"https://github.com/aytzey/showagent/releases/download/{TAG}/{name}")
            self.assertEqual(package["fileSha256"], hashlib.sha256((self.dist / name).read_bytes()).hexdigest())
        self.run_script("verify", "--tag", TAG, "--dist", self.dist)

    def test_missing_platform_aborts_before_manifest_is_created(self):
        self.make_target("linux_amd64")
        result = self.run_script("finalize", "--tag", TAG, "--dist", self.dist,
                                 "--server-template", self.template, success=False)
        self.assertIn("missing", result.stderr.lower())
        self.assertFalse((self.dist / "server.json").exists())

    def test_mismatched_binary_archive_aborts_publication(self):
        self.make_release()
        (self.dist / f"showagent_{TAG}_linux_amd64").write_bytes(b"wrong build")
        result = self.run_script("finalize", "--tag", TAG, "--dist", self.dist,
                                 "--server-template", self.template, success=False)
        self.assertIn("binary", result.stderr.lower())

    def test_tampered_checksum_is_rejected(self):
        self.make_release()
        checksum_file = self.dist / "SHA256SUMS"
        text = checksum_file.read_text(encoding="utf-8")
        checksum_file.write_text("0" * 64 + text[64:], encoding="utf-8")
        self.assertIn("checksum", self.run_script("verify", "--tag", TAG, "--dist", self.dist,
                                                  success=False).stderr.lower())

    def test_registry_hash_mismatch_is_rejected_even_after_rechecksumming(self):
        self.make_release()
        manifest_file = self.dist / "server.json"
        manifest = json.loads(manifest_file.read_text(encoding="utf-8"))
        manifest["packages"][0]["fileSha256"] = "0" * 64
        manifest_file.write_text(json.dumps(manifest), encoding="utf-8")
        checksums = "".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
                            for path in sorted(self.dist.iterdir()) if path.name != "SHA256SUMS")
        (self.dist / "SHA256SUMS").write_text(checksums, encoding="utf-8")
        self.assertIn("registry", self.run_script("verify", "--tag", TAG, "--dist", self.dist,
                                                  success=False).stderr.lower())

    def test_unexpected_release_file_is_rejected(self):
        self.make_release()
        (self.dist / "unexpected.txt").write_text("not a release asset", encoding="utf-8")
        self.assertIn("unexpected", self.run_script("verify", "--tag", TAG, "--dist", self.dist,
                                                    success=False).stderr.lower())

    def test_invalid_tag_is_rejected(self):
        result = self.run_script("finalize", "--tag", "v1.2.3/other", "--dist", self.dist,
                                 "--server-template", self.template, success=False)
        self.assertIn("tag", result.stderr.lower())

    def test_bundle_binary_mismatch_is_rejected(self):
        self.make_release()
        path = self.dist / f"showagent_{TAG}_linux_amd64.mcpb"
        with zipfile.ZipFile(path) as original:
            files = [(info, original.read(info.filename)) for info in original.infolist()]
        with zipfile.ZipFile(path, "w") as modified:
            for info, data in files:
                modified.writestr(info, b"different build" if info.filename == "server/showagent" else data)
        self.assertIn("bundle binary", self.run_script(
            "finalize", "--tag", TAG, "--dist", self.dist, "--server-template", self.template,
            success=False).stderr.lower())

    def test_pack_missing_binary_does_not_create_bundle(self):
        self.run_script("pack", "--tag", TAG, "--goos", "linux", "--goarch", "amd64",
                        "--binary", self.root / "missing", "--dist", self.dist,
                        "--license", self.license, "--readme", self.readme, success=False)
        self.assertEqual(list(self.dist.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
