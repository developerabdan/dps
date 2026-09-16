#!/bin/sh
# Install dps, a readable `docker ps`.
#
#   curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh | sh
#
# The script checks that this machine can run dps, downloads the release built
# for the platform, checks it against the published checksums, installs one
# file, and runs it once. It reads uname, GitHub, Docker and the install
# directory, and nothing else. It never asks for a password.
#
# Environment:
#   DPS_VERSION      Tag to install, e.g. v0.2.0. Default: the latest release.
#   DPS_INSTALL_DIR  Directory to install into. Default: /usr/local/bin when it
#                    can be written without a password, else ~/.local/bin.
#   DPS_NO_SUDO      Set to 1 to never use sudo.
#   DPS_SKIP_CHECKS  Set to 1 to install without the requirement checks.
#   DPS_NO_RUN       Set to 1 to install without running dps afterwards.

set -eu

REPO="developerabdan/dps"
BIN="dps"

# Everything runs inside main(), called on the last line. A download that is cut
# short therefore does nothing, instead of running half a script.
main() {
	os=$(detect_os)
	arch=$(detect_arch)
	dir=$(install_dir)

	requirements "$os" "$arch" "$dir"

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

	place "$tmp/$BIN" "$dir"
	report "$dir" "$version"
	autorun "$dir"
}

# requirements prints one line per thing dps needs and stops before downloading
# anything when a line fails. Every check is reported, not only the failing one:
# a person fixing two missing tools should learn both in one run.
requirements() {
	os=$1
	arch=$2
	dir=$3
	[ "${DPS_SKIP_CHECKS:-0}" = 1 ] && return 0

	fail=0
	say ""
	info "checking requirements"
	say ""

	check "$([ -n "$os" ] && echo 0 || echo 1)" os "${os:-$(uname -s) is not supported — dps builds for linux and darwin}"
	check "$([ -n "$arch" ] && echo 0 || echo 1)" arch "${arch:-$(uname -m) is not supported — dps builds for amd64 and arm64}"

	detail=$(downloader) && check 0 download "$detail" ||
		check 1 download "neither curl nor wget is installed"
	detail=$(checksummer) && check 0 checksum "$detail" ||
		check 1 checksum "no sha256sum and no shasum — install one, or set DPS_SKIP_CHECKSUM=1"
	if command -v tar >/dev/null 2>&1; then
		check 0 tar "$(command -v tar)"
	else
		check 1 tar "tar is not installed"
	fi

	detail=$(docker_engine) && check 0 docker "$detail" ||
		check 1 docker "$detail"

	if [ -w "$dir" ] || can_sudo; then
		check 0 install "$dir is writable"
	else
		check 1 install "cannot write $dir — set DPS_INSTALL_DIR to a directory you own"
	fi

	say ""
	[ "$fail" = 0 ] || err "requirements not met — fix the lines marked [x] and run this again"
}

# check prints one checklist line. A failing line sets fail, which requirements
# reads after every check has had its say.
check() {
	if [ "$1" = 0 ]; then
		printf '  [v] %-9s %s\n' "$2" "$3" >&2
	else
		printf '  [x] %-9s %s\n' "$2" "$3" >&2
		fail=1
	fi
}

# docker_engine decides whether a daemon will answer dps. The CLI is asked
# first because it already resolves contexts; without it the socket is pinged
# directly. A socket that cannot be pinged — no curl on the machine — is
# accepted and said so, because unprobed is not the same as down.
docker_engine() {
	if command -v docker >/dev/null 2>&1; then
		server=$(docker version --format '{{.Server.Version}}' 2>/dev/null) || server=""
		if [ -n "$server" ]; then
			echo "engine $server"
			return 0
		fi
	fi

	case ${DOCKER_HOST:-} in
	unix://*) sock=${DOCKER_HOST#unix://} ;;
	?*)
		echo "DOCKER_HOST is $DOCKER_HOST — dps speaks to unix sockets only"
		return 1
		;;
	*) sock=$(docker_socket) ;;
	esac

	if [ -z "$sock" ] || [ ! -S "$sock" ]; then
		if command -v docker >/dev/null 2>&1; then
			echo "docker is installed but no daemon is running"
		else
			echo "Docker Engine is not installed"
		fi
		return 1
	fi

	if command -v curl >/dev/null 2>&1; then
		if curl -s -o /dev/null --max-time 3 --unix-socket "$sock" http://localhost/_ping; then
			echo "daemon at $sock"
			return 0
		fi
		echo "socket $sock exists but the daemon does not answer"
		return 1
	fi

	echo "socket $sock (not probed — no curl to ping it with)"
	return 0
}

# docker_socket guesses where the daemon listens. dps itself reads the active
# Docker context to be exact; this only has to be right often enough to keep a
# working machine from being told it has no Docker, and the CLI check above
# covers the cases it misses.
docker_socket() {
	for s in "${HOME:-}/.docker/run/docker.sock" /var/run/docker.sock; do
		[ -S "$s" ] && {
			printf '%s\n' "$s"
			return 0
		}
	done
	printf '\n'
}

downloader() {
	if command -v curl >/dev/null 2>&1; then
		echo "curl"
	elif command -v wget >/dev/null 2>&1; then
		echo "wget"
	else
		return 1
	fi
}

checksummer() {
	if command -v sha256sum >/dev/null 2>&1; then
		echo "sha256sum"
	elif command -v shasum >/dev/null 2>&1; then
		echo "shasum"
	elif [ "${DPS_SKIP_CHECKSUM:-0}" = 1 ]; then
		echo "skipped, as DPS_SKIP_CHECKSUM asks"
	else
		return 1
	fi
}

# detect_os and detect_arch print nothing on an unsupported platform. The empty
# answer becomes a [x] line in the checklist rather than an error thrown before
# the reader has seen the rest of the list.
detect_os() {
	case $(uname -s) in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	esac
}

detect_arch() {
	case $(uname -m) in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
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

# can_sudo is true only for sudo that needs no password. A password prompt in a
# piped script is worth avoiding, so a locked sudo simply sends the binary to
# $HOME instead, and report() says what to do next.
can_sudo() {
	[ "${DPS_NO_SUDO:-0}" = 1 ] && return 1
	command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1
}

place() {
	from=$1
	dir=$2
	if [ -w "$dir" ]; then
		install -m 0755 "$from" "$dir/$BIN" || err "cannot write $dir/$BIN"
	else
		sudo -n install -m 0755 "$from" "$dir/$BIN" || err "cannot write $dir/$BIN"
	fi
}

# report is the last thing anyone reads, so it is short: where the binary is,
# and the one command that matters next.
report() {
	dir=$1
	version=$2
	installed=$("$dir/$BIN" --version 2>/dev/null || echo "$BIN $version")
	# ~ for reading, $HOME for pasting: a tilde inside the double quotes of an
	# export line is not expanded, and would set a literal "~/..." on PATH.
	short=$(printf '%s' "$dir" | sed "s|^$HOME/|~/|")
	literal=$(printf '%s' "$dir" | sed "s|^$HOME/|\$HOME/|")

	say ""
	case ":$PATH:" in
	*":$dir:"*)
		say "$installed installed in $short"
		say ""
		say "  dps       list containers"
		say "  dps -h    flags"
		;;
	*)
		say "$installed installed in $short — not on your PATH"
		say ""
		say "  run now:  $short/$BIN"
		say "  or fix:   echo 'export PATH=\"$literal:\$PATH\"' >> $(shell_rc) && . $(shell_rc)"
		;;
	esac
	say ""
}

# autorun starts dps once, so the first run happens here rather than being
# homework. On a machine with no config yet that first run is the setup wizard.
#
# The installer is usually read from a pipe, which leaves the script's stdin
# unusable for a program that reads keys, so the terminal is handed over
# directly. No terminal means nothing to show, and the run is skipped.
autorun() {
	dir=$1
	[ "${DPS_NO_RUN:-0}" = 1 ] && return 0
	[ -t 1 ] || return 0
	[ -r /dev/tty ] || return 0
	"$dir/$BIN" </dev/tty || true
}

shell_rc() {
	case ${SHELL##*/} in
	zsh) echo "~/.zshrc" ;;
	bash) echo "~/.bashrc" ;;
	ksh) echo "~/.kshrc" ;;
	*) echo "~/.profile" ;;
	esac
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
say() { printf '%s\n' "$1" >&2; }
err() {
	printf '%s: %s\n' "$BIN" "$1" >&2
	exit 1
}

main "$@"
