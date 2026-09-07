# Distribution and release verification

Use the [release archives](https://github.com/aytzey/showagent/releases) or the
shell installer for the published CLI. Confirm `showagent --version` and use the
README from that tag when reproducing a command. `main` may document work that
has not been released yet.

## Channel snapshot

Checked on 2026-09-07; these are observations, not pinned upgrade targets.

| Channel | Observed version | What to verify |
|---|---|---|
| GitHub latest / shell installer | v0.11.3 | All 17 published files downloaded and verified; [release workflow](https://github.com/aytzey/showagent/actions/runs/34168549236) passed |
| `go install ...@latest` | Resolved Go module tag | `showagent --version` and `showagent --help`; this installs a tagged module, not the current `main` checkout |
| [Homebrew tap](https://github.com/aytzey/homebrew-tap/blob/main/Formula/showagent.rb) | 0.11.3 | [Linux and macOS install/test checks](https://github.com/aytzey/homebrew-tap/actions/runs/34168872534) passed against the published archives |
| Published MCPB files, MCP Registry and repository `server.json` | 0.11.3 | Five bundles use the same binaries as CLI downloads; [registry publication](https://github.com/aytzey/showagent/actions/runs/34168794428) passed and the live API reports this version as active/latest |

The v0.11.0 CLI does not have the newer `transcript` CLI command. Its MCP server
already exposes `get_transcript`; missing a CLI subcommand does **not** mean the
MCP transcript tool is absent. A desktop bundle installs an MCP server for its
host application; it does not install the interactive CLI onto the user's PATH.
Compare the feature actually advertised by each channel, rather than requiring
unrelated packages to have identical version numbers.

## What the release workflow builds

The tag workflow builds Linux and macOS `amd64`/`arm64` and experimental Windows
`amd64`. Each target has an archive, a standalone binary, and a `.mcpb` bundle.
Python 3.10+ is a release/test dependency only; the installed Go binary needs no
Python runtime. The MCPB ZIP contains the same binary bytes as the CLI archive,
a manifest, the license, and the README from the tagged source.

Bundles follow the [MCPB binary manifest specification](https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md).
Their command is the bundled executable with `mcp`, not an executable looked up
on PATH. The manifest uses `win32` for Windows and records the supported OS.
Choose the filename matching your CPU architecture as well; MCPB v0.3's platform
field does not encode CPU selection. Linux bundle availability also does not
promise that a particular desktop host supports Linux.

From a repository checkout, after building the matching binary:

```sh
python3 scripts/release_artifacts.py pack \
  --tag vX.Y.Z --goos linux --goarch amd64 \
  --binary stage/showagent --dist dist
```

Replace `vX.Y.Z` with the actual numeric release tag. The script accepts only
stable `vMAJOR.MINOR.PATCH` tags, matching the release workflow. It never builds,
downloads, executes, or publishes a binary.

After all five targets' archives, standalone binaries and bundles are in `dist`:

```sh
python3 scripts/release_artifacts.py finalize --tag vX.Y.Z --dist dist
python3 scripts/release_artifacts.py verify --tag vX.Y.Z --dist dist
```

`finalize` rejects missing, unexpected, empty or inconsistent payloads before
generating `dist/server.json` and `dist/SHA256SUMS`. The registry manifest's
version and immutable release URLs come from the tag; its hashes come from the
actual bundles. `verify` checks the complete inventory, checksums, registry URLs
and package hashes, native entry points, executable permissions, and byte
equality between the archives, bundles and standalone binaries. It reads archive
members without extracting their paths. It is a release contract checker, not a
general-purpose MCPB or JSON Schema validator.

The workflow also runs the Linux/amd64 release binary's version check and the
isolated `scripts/verify-handoff.py` fixture smoke test. Payload equality checks
cover every packaged target; cross-compilation and archive checks are **not**
native runtime tests on macOS, ARM or Windows. The fixture smoke does not prove
an authenticated agent can continue the conversation; see
[compatibility evidence](compatibility.md).

## Publishing the other channels

`dist/server.json` is a release artifact for later registry publication. The
committed root `server.json` intentionally remains a snapshot of already
published bundles. Do not update it to future URLs or submit the new registry
manifest until the GitHub release and every referenced bundle are publicly
available and their hashes have been checked. The tag workflow does not publish
to the MCP Registry or update the Homebrew tap.

For the registry, dispatch `publish-mcp.yml` from `main` with the existing stable
release tag. This separate workflow downloads and verifies the entire published
artifact set, validates `server.json` with a pinned official publisher, then
uses GitHub OIDC to publish under this repository's namespace. It needs no
stored personal token and rejects draft or prerelease assets. A successful
GitHub release alone does not mean this registry step has run.

After the release exists, prepare the separate Homebrew formula change using
the four published `.tar.gz` URLs and their actual SHA256SUMS entries. Review and
test the formula in Homebrew before publishing it. Neither a successful local
packaging run nor editing a formula patch changes the installed public channel.

## Local checks

```sh
python3 -B -m unittest discover -s scripts -p test_release_artifacts.py
python3 scripts/verify-handoff.py --binary /absolute/path/to/showagent
```

The packaging tests use disposable fake binary bytes and synthetic documents;
they do not launch a coding agent or inspect user sessions. Meaningful failure
cases include absent platform files, differing bundle/archive binary bytes,
invalid tags, unexpected assets, changed checksums and a registry hash mismatch
even after the enclosing manifest is rechecksummed.
