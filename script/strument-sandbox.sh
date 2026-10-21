#!/usr/bin/env bash
# Run a command under the filesystem policy Strument applies to itself, using
# Bubblewrap instead of Landlock, so that another agent (or any command) can be
# confined the way Strument confines the commands its model runs.
#
# The policy is internal/sandbox: read and execute everywhere, write only to
# the project, temp directories, this machine's toolchain caches, a few
# harmless devices, and whatever --write adds. With --filter-env, the command
# also gets only the environment allowlist of internal/coder/envallow.go.
#
# Written for Bash 5.2 and bubblewrap 0.9 on Ubuntu 24.04. See --help.
#
# Where it differs from Strument's Landlock sandbox:
#
#   - It is a mount namespace, not a Landlock ruleset: writes outside the set
#     fail with EROFS ("Read-only file system") instead of EACCES.
#   - /dev is a fresh minimal one (see "Devices" below), so devices outside
#     that set are absent rather than read-only, and files created in /dev
#     stay in a private tmpfs.
#   - Without root, bwrap runs in a user namespace: files owned by other users
#     show as nobody, and setuid programs such as sudo do not work.
#   - The writable set is computed once, when the script starts, as Strument
#     computes it once at startup.
#
# The writable set matched sandbox.DefaultWritable path for path on a test
# home with every awkward case writable.go handles: cargo and go bin/ beside
# their caches, pnpm on PATH, pyenv and mise shims, a symlinked subdirectory,
# relocated caches, and a git worktree.
set -euo pipefail

prog=${0##*/}

usage() {
	cat <<EOF
Usage: $prog [options] [--] command [args...]

Run command with writes confined to Strument's sandbox policy: the project,
TMPDIR and /tmp, existing toolchain caches, a few devices, and --write paths.
Reading and executing stay unrestricted. The network is not restricted.

Options:
  -p, --project DIR     Project root (default: the git toplevel of the current
                        directory, else the current directory).
  -w, --write PATH      Also allow writing under PATH, like Strument's
                        sandbox_write. Absolute; skipped if it does not exist.
                        Repeatable.
  -e, --filter-env      Pass only Strument's environment allowlist, as it does
                        for commands the model runs. Off by default, because
                        an agent needs its own API key.
  -a, --env-allow NAME  Also pass NAME under --filter-env, like env_allow.
                        Repeatable.
  -l, --list            Print the writable set and exit, like /sandbox.
  -n, --dry-run         Print the bwrap command instead of running it.
  -h, --help            Show this help.

An agent also needs to write its own state. For example:
  $prog -w ~/.claude -w ~/.claude.json -- claude
  $prog -w ~/.codex -- codex
EOF
}

die() {
	printf '%s: %s\n' "$prog" "$*" >&2
	exit 2
}

project=
extra_write=()
filter_env=0
env_allow=()
list=0
dry_run=0

while (($#)); do
	case $1 in
	-p | --project)
		(($# >= 2)) || die "$1 needs a directory"
		project=$2
		shift 2
		;;
	-w | --write)
		(($# >= 2)) || die "$1 needs a path"
		[[ $2 == /* ]] || die "--write needs an absolute path, not '$2'"
		extra_write+=("$2")
		shift 2
		;;
	-e | --filter-env)
		filter_env=1
		shift
		;;
	-a | --env-allow)
		(($# >= 2)) || die "$1 needs a variable name"
		env_allow+=("$2")
		shift 2
		;;
	-l | --list)
		list=1
		shift
		;;
	-n | --dry-run)
		dry_run=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		break
		;;
	-*) die "unknown option '$1' (see --help)" ;;
	*) break ;;
	esac
done

if ((!list && !$#)); then
	usage >&2
	exit 2
fi

if [[ -z $project ]]; then
	project=$(git rev-parse --show-toplevel 2>/dev/null) || project=$PWD
fi
[[ -d $project ]] || die "project root '$project' is not a directory"
project=$(cd "$project" && pwd)

home=${HOME:-}

# The writable set, in the order of sandbox.DefaultWritable. Strument also
# grants its own state directory; another agent's goes in with --write.
writable=()
add() {
	local p
	for p; do
		if [[ -n $p ]]; then
			writable+=("$p")
		fi
	done
}

# git_dir mirrors gitDir in internal/sandbox/writable.go: when .git is a file
# (a worktree or a submodule), the directory it points to must be writable or
# every commit fails. For a worktree, that is the main repository's .git.
git_dir() {
	local dot=$1/.git line path base
	[[ -f $dot && ! -L $dot ]] || return 0
	IFS= read -r line <"$dot" || true
	[[ $line == gitdir:* ]] || return 0
	path=${line#gitdir:}
	path=${path#"${path%%[![:space:]]*}"}
	[[ $path == /* ]] || path=$1/$path
	base=$(dirname -- "$(dirname -- "$path")")
	if [[ ${base##*/} == .git ]]; then
		printf '%s\n' "$base"
	else
		printf '%s\n' "$path"
	fi
}

# Directories whose contents run by name are never granted, even inside a
# toolchain root: PATH entries (and their resolved targets), plus bin, sbin
# and shims by name. See executableDirs and writableSubdirs.
declare -A on_path=()
IFS=: read -r -a path_dirs <<<"${PATH:-}"
for d in "${path_dirs[@]}"; do
	[[ $d == /* ]] || continue
	on_path[$d]=1
	if r=$(realpath -e -- "$d" 2>/dev/null); then
		on_path[$r]=1
	fi
done

# writable_subdirs grants a toolchain root's subdirectories, never the root:
# ~/.cargo holds registry/ beside bin/, and pnpm keeps its store inside the
# directory on PATH. Symlinked subdirectories are skipped, since binding one
# grants wherever it points.
writable_subdirs() {
	local root=$1 sub name r
	[[ -n $root && -d $root ]] || return 0
	for sub in "$root"/* "$root"/.[!.]*; do
		[[ -d $sub && ! -L $sub ]] || continue
		name=${sub##*/}
		case $name in bin | sbin | shims) continue ;; esac
		if [[ -n ${on_path[$sub]:-} ]]; then
			continue
		fi
		if r=$(realpath -e -- "$sub" 2>/dev/null) && [[ -n ${on_path[$r]:-} ]]; then
			continue
		fi
		printf '%s\n' "$sub"
	done
}

# pick reads a toolchain's own variable before falling back, so a relocated
# cache is respected.
pick() {
	local name=$1 fallback=${2:-}
	printf '%s\n' "${!name:-$fallback}"
}
under() {
	[[ -n $home ]] || return 0
	local IFS=/
	printf '%s/%s\n' "$home" "$*"
}

add "$project"
add "${extra_write[@]}"
add "$(git_dir "$project")"

# Temp: TMPDIR when set, and /tmp either way.
add "${TMPDIR:-/tmp}" /tmp

# Toolchain caches: the same list as cacheDirs, in the same order.
for root in \
	"$(pick GOPATH "$(under go)")" \
	"$(pick CARGO_HOME "$(under .cargo)")" \
	"$(pick PNPM_HOME "$(under .local share pnpm)")" \
	"$(pick BUN_INSTALL "$(under .bun)")" \
	"$(pick DENO_INSTALL_ROOT "$(under .deno)")" \
	"$(under .yarn)" \
	"$(pick GEM_HOME "$(under .gem)")" \
	"$(pick CABAL_DIR "$(under .cabal)")" \
	"$(pick DOTNET_CLI_HOME "$(under .dotnet)")" \
	"$(pick FOUNDRY_DIR "$(under .foundry)")" \
	"$(pick PYENV_ROOT "$(under .pyenv)")" \
	"$(pick MISE_DATA_DIR "$(under .local share mise)")"; do
	mapfile -t subs < <(writable_subdirs "$root")
	add "${subs[@]}"
done
add \
	"$(pick XDG_CACHE_HOME "$(under .cache)")" \
	"$(pick GOCACHE)" \
	"$(pick GOMODCACHE)" \
	"$(pick RUSTUP_HOME "$(under .rustup)")" \
	"$(pick PIP_CACHE_DIR)" \
	"$(pick UV_CACHE_DIR)" \
	"$(pick POETRY_CACHE_DIR)" \
	"$(pick npm_config_cache "$(under .npm)")" \
	"$(pick YARN_CACHE_FOLDER)" \
	"$(pick DENO_DIR)" \
	"$(under .m2)" \
	"$(pick GRADLE_USER_HOME "$(under .gradle)")" \
	"$(pick NUGET_PACKAGES "$(under .nuget packages)")" \
	"$(pick COMPOSER_HOME "$(under .composer)")" \
	"$(pick COMPOSER_CACHE_DIR)" \
	"$(pick BUNDLE_USER_HOME "$(under .bundle)")" \
	"$(pick MIX_HOME "$(under .mix)")" \
	"$(pick HEX_HOME "$(under .hex)")" \
	"$(pick SHARDS_CACHE_PATH)" \
	"$(pick STACK_ROOT "$(under .stack)")" \
	"$(under .swiftpm)" \
	"$(pick OPAMROOT "$(under .opam)")" \
	"$(pick DUB_HOME "$(under .dub)")" \
	"$(pick PUB_CACHE "$(under .pub-cache)")" \
	"$(under .local share racket)" \
	"$(under .lein)" \
	"$(under .luarocks)" \
	"$(under .eldev)" \
	"$(pick R_LIBS_USER)" \
	"$(pick MISE_CACHE_DIR)"

# Devices: Strument grants writing to /dev/null, zero, full, tty, ptmx, pts
# and shm, and reading everything. bwrap's --dev builds a fresh /dev with
# exactly that writable set (plus random and urandom) and a private devpts, so
# pseudo-terminals work. The host's /dev/shm and /dev/log are bound on top,
# so shared memory and syslog behave as they do outside. Other devices are
# absent rather than read-only. The ones worth having (GPUs, /dev/kvm,
# /dev/fuse) must be opened read-write, which Strument denies too.
devices=(/dev/shm /dev/log)

# Resolve and dedupe. A path that does not exist is skipped, as Strument skips
# it. Binding the resolved path matches Landlock, which anchors a rule to the
# inode a path resolves to.
declare -A seen=()
grants=()
missing=()
for p in "${writable[@]}"; do
	if ! r=$(realpath -e -- "$p" 2>/dev/null); then
		missing+=("$p")
		continue
	fi
	if [[ -n ${seen[$r]:-} ]]; then
		continue
	fi
	seen[$r]=1
	grants+=("$r")
done

if ((list)); then
	printf 'Writable (%d):\n' "${#grants[@]}"
	printf '  %s\n' "${grants[@]}"
	printf 'Devices: a fresh /dev (null, zero, full, random, urandom, tty, ptmx, pts)'
	for d in "${devices[@]}"; do
		if [[ -e $d ]]; then
			printf ', %s' "$d"
		fi
	done
	printf '\n'
	if ((${#missing[@]})); then
		printf 'Not granted, because they do not exist:\n'
		printf '  %s\n' "${missing[@]}"
	fi
	exit 0
fi

command -v bwrap >/dev/null || die "bwrap not found; install it with: sudo apt install bubblewrap"

# / read-only, then a fresh /dev, then each grant. bwrap mounts every bind
# but --dev-bind nodev, and so does --remount-ro: binding the host's /dev and
# making it read-only stopped programs from reading /dev/urandom, and git from
# writing any object.
args=(--ro-bind / / --dev /dev)
for d in "${devices[@]}"; do
	if [[ -e $d ]]; then
		args+=(--bind "$d" "$d")
	fi
done
for g in "${grants[@]}"; do
	args+=(--bind "$g" "$g")
done
args+=(--die-with-parent --chdir "$PWD")

if ((filter_env)); then
	# The default allowlist of internal/coder/envallow.go, Linux names only.
	allow=(
		PATH HOME SHELL USER LOGNAME TERM TERMINFO COLUMNS LINES TMPDIR PWD
		LANG LANGUAGE TZ
		LC_ALL LC_CTYPE LC_MESSAGES LC_NUMERIC LC_TIME LC_COLLATE LC_MONETARY
		LC_PAPER LC_NAME LC_ADDRESS LC_TELEPHONE LC_MEASUREMENT LC_IDENTIFICATION
		HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY
		http_proxy https_proxy all_proxy no_proxy SOCKS5_SERVER
		XDG_CONFIG_HOME XDG_CACHE_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_RUNTIME_DIR
		GOROOT GOPATH GOCACHE GOMODCACHE GOENV GOFLAGS GOTOOLCHAIN GOPROXY
		GOPRIVATE GONOPROXY GOSUMDB GONOSUMDB GOINSECURE GOTOOLDIR GOOS GOARCH
		GOARM GO386 GOAMD64 GORACE GOTRACEBACK GODEBUG GOMEMLIMIT GOMAXPROCS
		GOTELEMETRYDIR GOCOVERDIR GOEXPERIMENT GOWASM GOFIPS140
		CGO_ENABLED CC CXX
		CARGO_HOME RUSTUP_HOME RUST_BACKTRACE RUSTFLAGS
		VIRTUAL_ENV PIP_CACHE_DIR UV_CACHE_DIR UV_PYTHON_INSTALL_DIR POETRY_CACHE_DIR
		JAVA_HOME GRADLE_USER_HOME
		"${env_allow[@]}"
	)
	args+=(--clearenv)
	for name in "${allow[@]}"; do
		if [[ $name != PWD && -v $name ]]; then
			args+=(--setenv "$name" "${!name}")
		fi
	done
	args+=(--setenv PWD "$PWD")
fi

if ((dry_run)); then
	printf '%q ' bwrap "${args[@]}" -- "$@"
	printf '\n'
	exit 0
fi

# Ubuntu 24.04 restricts unprivileged user namespaces through AppArmor, and
# bwrap needs one when it does not run as root. Say how to allow it rather than
# leave the user with "setting up uid map: Permission denied".
if ((EUID != 0)) && [[ $(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null) == 1 ]] &&
	! bwrap --ro-bind / / -- true 2>/dev/null; then
	cat >&2 <<'EOF'
strument-sandbox.sh: bwrap cannot create a user namespace. Ubuntu 24.04
restricts them with AppArmor; allow bwrap with a profile, then reload:

  sudo tee /etc/apparmor.d/bwrap <<'END'
  abi <abi/4.0>,
  include <tunables/global>
  profile bwrap /usr/bin/bwrap flags=(unconfined) {
    userns,
    include if exists <local/bwrap>
  }
  END
  sudo apparmor_parser -r /etc/apparmor.d/bwrap
EOF
	exit 1
fi

exec bwrap "${args[@]}" -- "$@"
