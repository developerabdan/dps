#!/bin/sh
# Install dps, a readable `docker ps`.
#
#   curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh | sh
#
# The script downloads the release built for this platform, checks it against
# the published checksums, and installs one file. It reads uname, GitHub and
# the install directory, and nothing else. It never touches Docker.
#
# Environment:
#   DPS_VERSION      Tag to install, e.g. v0.2.0. Default: the latest release.
#   DPS_INSTALL_DIR  Directory to install into. Default: /usr/local/bin, or
#                    ~/.local/bin when /usr/local/bin cannot be written.

set -eu

REPO="developerabdan/dps"
BIN="dps"

# Everything runs inside main(), called on the last line. A download that is cut
# short therefore does nothing, instead of running half a script.
main() {
	os=$(detect_os)
	arch=$(detect_arch)
	version=${DPS_VERSION:-$(latest_version)}
	[ -n "$version" ] || err "cannot read the latest release tag from GitHub"

	asset="${BIN}_${version#v}_${os}_${arch}.tar.gz"
	base="https://github.com/$REPO/releases/download/$version"

	tmp=$(mktemp -d) || err "cannot create a temporary directory"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	info "downloading $asset"
	download "$base/$asset" "$tmp/$asset" ||
		err "no release asset $asset — see https://github.com/$REPO/releases"
	download "$base/checksums.txt" "$tmp/checksums.txt" ||
		err "cannot download checksums.txt for $version"

	verify "$tmp" "$asset"
	tar -xzf "$tmp/$asset" -C "$tmp" || err "cannot unpack $asset"
	[ -f "$tmp/$BIN" ] || err "$asset does not contain a $BIN binary"

	dir=$(install_dir)
	place "$tmp/$BIN" "$dir"

	info "installed $("$dir/$BIN" --version 2>/dev/null || echo "$BIN $version") in $dir"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) info "$dir is not on your PATH — add it, or move $dir/$BIN somewhere that is" ;;
	esac
}

detect_os() {
	case $(uname -s) in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*) err "$(uname -s) is not supported — dps builds for linux and darwin" ;;
	esac
}

detect_arch() {
	case $(uname -m) in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) err "$(uname -m) is not supported — dps builds for amd64 and arm64" ;;
	esac
}

latest_version() {
	fetch "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' |
		head -n 1
}

# verify compares the downloaded asset with its line in checksums.txt. A missing
# sha256 tool stops the install; set DPS_SKIP_CHECKSUM=1 to accept that risk.
verify() {
	dir=$1
	name=$2

	expected=$(sed -n "s/^\([0-9a-f]*\)  *$name\$/\1/p" "$dir/checksums.txt" | head -n 1)
	[ -n "$expected" ] || err "checksums.txt has no line for $name"

	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$dir/$name" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$dir/$name" | cut -d' ' -f1)
	elif [ "${DPS_SKIP_CHECKSUM:-0}" = 1 ]; then
		info "no sha256 tool — skipping the checksum, as DPS_SKIP_CHECKSUM asks"
		return 0
	else
		err "no sha256sum and no shasum — install one, or set DPS_SKIP_CHECKSUM=1"
	fi

	[ "$actual" = "$expected" ] ||
		err "checksum mismatch for $name: expected $expected, got $actual"
}

install_dir() {
	if [ -n "${DPS_INSTALL_DIR:-}" ]; then
		mkdir -p "$DPS_INSTALL_DIR" || err "cannot create $DPS_INSTALL_DIR"
		echo "$DPS_INSTALL_DIR"
		return
	fi
	if [ -w /usr/local/bin ] || can_sudo; then
		echo /usr/local/bin
		return
	fi
	mkdir -p "$HOME/.local/bin" || err "cannot create $HOME/.local/bin"
	echo "$HOME/.local/bin"
}

place() {
	from=$1
	dir=$2
	if [ -w "$dir" ]; then
		install -m 0755 "$from" "$dir/$BIN" || err "cannot write $dir/$BIN"
	else
		info "$dir needs root — running sudo install"
		sudo install -m 0755 "$from" "$dir/$BIN" || err "cannot write $dir/$BIN"
	fi
}

can_sudo() {
	command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1
}

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		err "neither curl nor wget is installed"
	fi
}

download() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		err "neither curl nor wget is installed"
	fi
}

info() { printf '%s: %s\n' "$BIN" "$1" >&2; }
err() {
	printf '%s: %s\n' "$BIN" "$1" >&2
	exit 1
}

main "$@"
