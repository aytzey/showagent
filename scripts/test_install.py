"""Exercise the shell installer with disposable release fixtures and no network."""

import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("install.sh").resolve()
SHELL = os.environ.get("BASH_BIN") or shutil.which("sh")
RELEASES = "https://github.com/aytzey/showagent/releases"
TAG = "v1.2.3"
ARCHIVE = f"showagent_{TAG}_linux_amd64.tar.gz"
BINARY = b"fixture binary\n"


@unittest.skipUnless(SHELL, "a POSIX shell is required")
class InstallerTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="showagent-install-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.destination = self.root / "install dir"
        self.requests = self.root / "requests"
        self.stub("uname", 'case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac\n')
        self.stub("curl", """\
set -eu
output=""
follow=false
while [ "$#" -gt 1 ]; do
    case "$1" in
        -o|--output) output="$2"; shift ;;
        -L|-fL|--location) follow=true ;;
        --no-location) follow=false ;;
    esac
    shift
done
printf '%s\\n' "$1" >> requests
case "$1" in
    https://api.github.com/repos/aytzey/showagent/releases/latest)
        printf '%s\\n' '{"tag_name":"v1.2.3"}' ;;
    https://github.com/aytzey/showagent/releases/latest)
        [ "$follow" = false ] || exit 1
        printf '%s' "$TEST_REDIRECT"
        exit "$TEST_REDIRECT_STATUS" ;;
    https://github.com/aytzey/showagent/releases/download/v1.2.3/*)
        cp "${1##*/}" "$output" ;;
    *) exit 1 ;;
esac
""")
        archive_path = self.root / ARCHIVE
        with tarfile.open(archive_path, "w:gz") as archive:
            entry = tarfile.TarInfo("showagent")
            entry.size = len(BINARY)
            entry.mode = 0o755
            archive.addfile(entry, io.BytesIO(BINARY))
        checksum = hashlib.sha256(archive_path.read_bytes()).hexdigest()
        (self.root / "SHA256SUMS").write_bytes(f"{checksum}  {ARCHIVE}\n".encode())

    def stub(self, name, body):
        path = self.bin / name
        path.write_bytes(("#!/bin/sh\n" + body).encode())
        path.chmod(0o755)

    def run_installer(self, tag="", redirect=f"{RELEASES}/tag/{TAG}", redirect_status=0):
        self.requests.unlink(missing_ok=True)
        return subprocess.run(
            [SHELL, "-c", 'cd "$1" && export PATH="$PWD/bin:$PATH" && exec sh "$2"',
             "test-install", self.root.as_posix(), SCRIPT.as_posix()],
            env={**os.environ, "SHOWAGENT_VERSION": tag, "SHOWAGENT_INSTALL_DIR": self.destination.as_posix(),
                 "TEST_REDIRECT": redirect, "TEST_REDIRECT_STATUS": str(redirect_status)},
            capture_output=True, text=True, check=False, timeout=15,
        )

    def assert_installed(self, result, expected_requests):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.destination / "showagent").read_bytes(), BINARY)
        self.assertEqual(self.requests.read_text().splitlines(), expected_requests)

    def test_latest_redirect_selects_a_pinned_archive_and_checksum(self):
        self.assert_installed(self.run_installer(), [
            f"{RELEASES}/latest",
            f"{RELEASES}/download/{TAG}/{ARCHIVE}",
            f"{RELEASES}/download/{TAG}/SHA256SUMS",
        ])

    def test_explicit_version_skips_latest_lookup(self):
        self.assert_installed(self.run_installer(tag=TAG), [
            f"{RELEASES}/download/{TAG}/{ARCHIVE}",
            f"{RELEASES}/download/{TAG}/SHA256SUMS",
        ])

    def test_invalid_latest_redirect_stops_before_downloading(self):
        for redirect in ("", f"{RELEASES}/tag/{TAG}/extra", f"{RELEASES}/tag/{TAG}-rc.1",
                         f"{RELEASES.replace('aytzey', 'other')}/tag/{TAG}",
                         f"{RELEASES.replace('github.com', 'example.com')}/tag/{TAG}",
                         RELEASES.replace("https:", "http:") + f"/tag/{TAG}"):
            with self.subTest(redirect=redirect):
                result = self.run_installer(redirect=redirect)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("release tag", result.stderr)
                self.assertFalse(self.destination.exists())
                self.assertEqual(self.requests.read_text().splitlines(), [f"{RELEASES}/latest"])

    def test_failed_lookup_ignores_partial_output(self):
        result = self.run_installer(redirect_status=22)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release tag", result.stderr)
        self.assertFalse(self.destination.exists())
        self.assertEqual(self.requests.read_text().splitlines(), [f"{RELEASES}/latest"])
