#!/usr/bin/env bash
# Build (or take a prebuilt) safeshell, put it in ~/.local/bin, hook your shell, and
# offer the one-time sandbox permission and the built-in AI.
#   scripts/install.sh [--ai] [--yes]
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
ai=0 yes=0
for a in "$@"; do case "$a" in --ai) ai=1 ;; --yes) yes=1 ;; *) echo "unknown option $a" >&2; exit 2 ;; esac; done
ask() { [ "$yes" = 1 ] && return 0; read -r -p "$1 [y/N] " r; [ "$r" = y ] || [ "$r" = Y ]; }

if [ -x "$here/safeshell" ]; then src="$here/safeshell"
elif command -v go >/dev/null 2>&1; then (cd "$here" && make -s build) && src="$here/bin/safeshell"
else echo "Need either a prebuilt ./safeshell or Go to build it (https://go.dev/dl)." >&2; exit 1; fi

dest="$HOME/.local/bin/safeshell"
mkdir -p "$(dirname "$dest")"
install -m 0755 "$src" "$dest"
echo "Installed $dest"
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo "Note: add \$HOME/.local/bin to your PATH." ;; esac

if ! "$dest" doctor >/dev/null 2>&1; then
  echo "The sandbox needs a one-time permission on this system (Ubuntu 24.04+)."
  if ask "Install the AppArmor rule for $dest now (needs sudo)?"; then
    apparmor_args=("$dest")
    if [ "$yes" = 1 ]; then apparmor_args+=(--yes); fi
    "$here/scripts/install-apparmor.sh" "${apparmor_args[@]}"
  fi
fi

if [ "$ai" = 1 ]; then "$dest" install --ai; else "$dest" install; fi
