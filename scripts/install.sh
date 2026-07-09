#!/bin/sh
# Install the latest showagent release from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/aytzey/showagent/main/scripts/install.sh | sh
#
# Environment:
#   SHOWAGENT_INSTALL_DIR  install directory (default: ~/.local/bin,
#                          falls back to /usr/local/bin via sudo)
#   SHOWAGENT_VERSION      release tag to install (default: latest)

set -eu

REPO="aytzey/showagent"
MAX_METADATA_BYTES=1048576
MAX_CHECKSUM_BYTES=1048576
MAX_ARCHIVE_BYTES=67108864
MAX_BINARY_BYTES=67108864

fail() {
    echo "install.sh: $1" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

download() {
    curl -fL --retry 3 --retry-delay 1 --connect-timeout 10 --max-time 300 "$@"
}

case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) fail "unsupported OS: $(uname -s) (download a release archive manually from https://github.com/${REPO}/releases)" ;;
esac

case "$(uname -m)" in
    x86_64 | amd64) arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
esac

tag="${SHOWAGENT_VERSION:-}"
if [ -z "$tag" ]; then
    tag=$(download --max-filesize "$MAX_METADATA_BYTES" -sS "https://api.github.com/repos/${REPO}/releases/latest" |
        sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
fi
[ -n "$tag" ] || fail "could not determine the latest release tag"
printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
    fail "invalid release tag: ${tag}"

archive="showagent_${tag}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT INT TERM

echo "Downloading ${archive} (${tag})..."
download --max-filesize "$MAX_ARCHIVE_BYTES" -sS -o "${tmpdir}/${archive}" "${base_url}/${archive}" ||
    fail "download failed: ${base_url}/${archive}"
download --max-filesize "$MAX_CHECKSUM_BYTES" -sS -o "${tmpdir}/SHA256SUMS" "${base_url}/SHA256SUMS" ||
    fail "download failed: ${base_url}/SHA256SUMS"

archive_size=$(wc -c <"${tmpdir}/${archive}" | tr -d ' ')
[ "$archive_size" -le "$MAX_ARCHIVE_BYTES" ] ||
    fail "archive exceeds ${MAX_ARCHIVE_BYTES} bytes"
checksum_size=$(wc -c <"${tmpdir}/SHA256SUMS" | tr -d ' ')
[ "$checksum_size" -le "$MAX_CHECKSUM_BYTES" ] ||
    fail "SHA256SUMS exceeds ${MAX_CHECKSUM_BYTES} bytes"

expected=$(sed -n "s/^\([0-9a-f]\{64\}\)[[:space:]]\{1,\}\*\{0,1\}${archive}\$/\1/p" \
    "${tmpdir}/SHA256SUMS" | head -n 1)
[ -n "$expected" ] || fail "no SHA256SUMS entry for ${archive}"

if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmpdir}/${archive}" | cut -d ' ' -f 1)
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${tmpdir}/${archive}" | cut -d ' ' -f 1)
else
    fail "sha256sum or shasum is required to verify the download"
fi
[ "$actual" = "$expected" ] ||
    fail "checksum mismatch for ${archive}: expected ${expected}, got ${actual}"

tar -xzf "${tmpdir}/${archive}" -C "$tmpdir" showagent
binary_size=$(wc -c <"${tmpdir}/showagent" | tr -d ' ')
[ "$binary_size" -le "$MAX_BINARY_BYTES" ] ||
    fail "extracted binary exceeds ${MAX_BINARY_BYTES} bytes"

[ -n "${HOME:-}" ] || [ -n "${SHOWAGENT_INSTALL_DIR:-}" ] ||
    fail "HOME is not set; set SHOWAGENT_INSTALL_DIR"
use_sudo=""
if [ -n "${SHOWAGENT_INSTALL_DIR:-}" ]; then
    install_dir="$SHOWAGENT_INSTALL_DIR"
    mkdir -p "$install_dir" 2>/dev/null ||
        fail "cannot create SHOWAGENT_INSTALL_DIR: ${install_dir}"
    [ -w "$install_dir" ] ||
        fail "SHOWAGENT_INSTALL_DIR is not writable: ${install_dir}"
else
    install_dir="${HOME}/.local/bin"
    if ! mkdir -p "$install_dir" 2>/dev/null || [ ! -w "$install_dir" ]; then
        install_dir="/usr/local/bin"
        if [ ! -w "$install_dir" ]; then
            command -v sudo >/dev/null 2>&1 ||
                fail "cannot write to ${install_dir} and sudo is unavailable; set SHOWAGENT_INSTALL_DIR to a writable directory"
            use_sudo="sudo"
            echo "Installing to ${install_dir} (requires sudo)..."
        fi
    fi
fi

$use_sudo install -m 0755 "${tmpdir}/showagent" "${install_dir}/showagent"

echo "Installed showagent ${tag} to ${install_dir}/showagent"
case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *) echo "Note: ${install_dir} is not on your PATH" ;;
esac
