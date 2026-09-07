#!/usr/bin/env python3
"""Build and verify showagent release bundles with Python's standard library.

This script never downloads, executes, or publishes an artifact. MCPB follows
https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md (binary, v0.3).
"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import stat
import sys
import tarfile
import zipfile


TARGETS = (("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"),
           ("darwin", "arm64"), ("windows", "amd64"))
REPOSITORY = "https://github.com/aytzey/showagent"


def version(tag):
    if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", tag):
        raise ValueError("release tag must be vMAJOR.MINOR.PATCH")
    return tag[1:]


def stem(tag, goos, goarch):
    return f"showagent_{tag}_{goos}_{goarch}"


def executable(goos):
    return "showagent.exe" if goos == "windows" else "showagent"


def read_file(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing or non-regular artifact: {path.name}")
    data = path.read_bytes()
    if not data:
        raise ValueError(f"empty artifact: {path.name}")
    return data


def digest(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(value):
    return (json.dumps(value, indent=2, ensure_ascii=False) + "\n").encode("utf-8")


def bundle_manifest(tag, goos, goarch):
    entry = "server/" + executable(goos)
    return {
        "manifest_version": "0.3",
        "name": "showagent",
        "display_name": f"showagent ({goos}/{goarch})",
        "version": version(tag),
        "description": "Find local coding-agent sessions and transfer their conversation between supported agents.",
        "long_description": (
            "Search session metadata and first/last user messages, read transcripts, "
            "branch or convert user/assistant messages, and return resume commands. "
            "Original sessions are preserved; tool state and attachments are not transferred. "
            "Transcript secrets are redacted by default. The MCP client may send returned "
            "history to its model provider. Choose the bundle matching your OS and CPU."
        ),
        "author": {"name": "Aytug Berk Sezer", "url": "https://github.com/aytzey"},
        "repository": {"type": "git", "url": REPOSITORY},
        "homepage": REPOSITORY,
        "license": "MIT",
        "server": {
            "type": "binary", "entry_point": entry,
            "mcp_config": {"command": "${__dirname}/" + entry, "args": ["mcp"],
                           "env": {"SHOWAGENT_NO_UPDATE_CHECK": "1"}},
        },
        "tools_generated": True,
        "compatibility": {"platforms": ["win32" if goos == "windows" else goos]},
    }


def pack(tag, goos, goarch, binary, dist, license_file, readme):
    version(tag)
    if (goos, goarch) not in TARGETS:
        raise ValueError(f"unsupported release target: {goos}/{goarch}")
    entry = "server/" + executable(goos)
    files = {"manifest.json": json_bytes(bundle_manifest(tag, goos, goarch)),
             entry: read_file(binary), "LICENSE": read_file(license_file),
             "README.md": read_file(readme)}
    dist.mkdir(parents=True, exist_ok=True)
    output = dist / (stem(tag, goos, goarch) + ".mcpb")
    if output.is_symlink():
        raise ValueError("bundle output must not be a symlink")
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as bundle:
        for name, data in files.items():
            # Stable timestamps and explicit executable bits on every build host.
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (stat.S_IFREG | (0o755 if name == entry else 0o644)) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            bundle.writestr(info, data)
    return output


def expected_names(tag):
    names = set()
    for goos, goarch in TARGETS:
        base = stem(tag, goos, goarch)
        names.update((base + (".exe" if goos == "windows" else ""),
                      base + (".zip" if goos == "windows" else ".tar.gz"), base + ".mcpb"))
    return names


def check_inventory(dist, expected, optional=()):
    present = {path.name for path in dist.iterdir()}
    missing = expected - present
    extra = present - expected - set(optional)
    if missing:
        raise ValueError("missing release artifacts: " + ", ".join(sorted(missing)))
    if extra:
        raise ValueError("unexpected release artifacts: " + ", ".join(sorted(extra)))
    for name in present:
        read_file(dist / name)


def archive_files(path):
    """Read only known regular members; never extract paths from an archive."""
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            infos = archive.infolist()
            names = [info.filename for info in infos]
            if (len(names) != len(set(names))
                    or any(info.is_dir() or stat.S_ISLNK(info.external_attr >> 16) for info in infos)):
                raise ValueError(f"duplicate or non-file archive members: {path.name}")
            return {name: archive.read(name) for name in names}
    with tarfile.open(path, "r:gz") as archive:
        infos = archive.getmembers()
        names = [info.name for info in infos]
        if len(names) != len(set(names)) or any(not info.isfile() for info in infos):
            raise ValueError(f"duplicate or non-file archive members: {path.name}")
        if any(info.name == "showagent" and not info.mode & 0o111 for info in infos):
            raise ValueError(f"archive binary is not executable: {path.name}")
        return {info.name: archive.extractfile(info).read() for info in infos}


def check_payloads(tag, dist):
    for goos, goarch in TARGETS:
        base = stem(tag, goos, goarch)
        entry = executable(goos)
        binary = read_file(dist / (base + (".exe" if goos == "windows" else "")))
        archive = archive_files(dist / (base + (".zip" if goos == "windows" else ".tar.gz")))
        if set(archive) != {entry, "LICENSE", "README.md"}:
            raise ValueError(f"unexpected archive contents: {base}")
        if archive[entry] != binary:
            raise ValueError(f"archive binary differs from standalone binary: {base}")
        with zipfile.ZipFile(dist / (base + ".mcpb")) as bundle:
            names = bundle.namelist()
            expected = {"manifest.json", "server/" + entry, "LICENSE", "README.md"}
            if len(names) != len(expected) or set(names) != expected:
                raise ValueError(f"unexpected bundle contents: {base}")
            if json.loads(bundle.read("manifest.json")) != bundle_manifest(tag, goos, goarch):
                raise ValueError(f"bundle manifest version/platform/command mismatch: {base}")
            if bundle.read("server/" + entry) != binary:
                raise ValueError(f"bundle binary differs from standalone binary: {base}")
            if bundle.getinfo("server/" + entry).external_attr >> 16 & 0o777 != 0o755:
                raise ValueError(f"bundle binary is not executable: {base}")
            for name in ("LICENSE", "README.md"):
                if not archive[name] or bundle.read(name) != archive[name]:
                    raise ValueError(f"bundle/archive documentation mismatch: {base}/{name}")


def registry_packages(tag, dist):
    return [{"registryType": "mcpb",
             "identifier": f"{REPOSITORY}/releases/download/{tag}/{stem(tag, goos, goarch)}.mcpb",
             "version": version(tag),
             "fileSha256": digest(read_file(dist / (stem(tag, goos, goarch) + ".mcpb"))),
             "transport": {"type": "stdio"}}
            for goos, goarch in TARGETS]


def finalize(tag, dist, template):
    version(tag)
    check_inventory(dist, expected_names(tag), optional=("server.json", "SHA256SUMS"))
    check_payloads(tag, dist)
    manifest = json.loads(read_file(template))
    if template.resolve() == (dist / "server.json").resolve():
        raise ValueError("server template must remain outside the release directory")
    if manifest.get("name") != "io.github.aytzey/showagent":
        raise ValueError("unexpected registry server name")
    manifest["version"] = version(tag)
    manifest["packages"] = registry_packages(tag, dist)
    (dist / "server.json").write_bytes(json_bytes(manifest))
    names = expected_names(tag) | {"server.json"}
    checksums = "".join(f"{digest(read_file(dist / name))}  {name}\n" for name in sorted(names))
    (dist / "SHA256SUMS").write_bytes(checksums.encode("utf-8"))
    verify(tag, dist)


def verify(tag, dist):
    version(tag)
    names = expected_names(tag) | {"server.json"}
    check_inventory(dist, names | {"SHA256SUMS"})
    lines = read_file(dist / "SHA256SUMS").decode("utf-8").splitlines()
    checksums = {}
    for line in lines:
        match = re.fullmatch(r"([a-f0-9]{64})  ([A-Za-z0-9_.-]+)", line)
        if not match or match[2] in checksums:
            raise ValueError("malformed or duplicate checksum entry")
        checksums[match[2]] = match[1]
    if set(checksums) != names:
        raise ValueError("checksum inventory differs from release inventory")
    for name, expected in checksums.items():
        if digest(read_file(dist / name)) != expected:
            raise ValueError(f"checksum mismatch: {name}")
    manifest = json.loads(read_file(dist / "server.json"))
    if (manifest.get("name") != "io.github.aytzey/showagent"
            or manifest.get("version") != version(tag)
            or manifest.get("packages") != registry_packages(tag, dist)):
        raise ValueError("registry version, URL, or package hash mismatch")
    check_payloads(tag, dist)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("pack", "finalize", "verify"):
        child = commands.add_parser(command)
        child.add_argument("--tag", required=True)
        child.add_argument("--dist", type=Path, required=True)
        if command == "pack":
            child.add_argument("--goos", required=True)
            child.add_argument("--goarch", required=True)
            child.add_argument("--binary", type=Path, required=True)
            child.add_argument("--license", type=Path, default=Path("LICENSE"))
            child.add_argument("--readme", type=Path, default=Path("README.md"))
        elif command == "finalize":
            child.add_argument("--server-template", type=Path, default=Path("server.json"))
    args = parser.parse_args()
    try:
        if args.command == "pack":
            output = pack(args.tag, args.goos, args.goarch, args.binary, args.dist, args.license, args.readme)
            print(f"Packed {output.name}")
        elif args.command == "finalize":
            finalize(args.tag, args.dist, args.server_template)
            print(f"Validated five targets; generated {args.dist / 'server.json'} and SHA256SUMS")
        else:
            verify(args.tag, args.dist)
            print("Release inventory, checksums, registry, and bundle contents verified")
    except (OSError, ValueError, tarfile.TarError, zipfile.BadZipFile) as exc:
        print(f"release artifacts: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
