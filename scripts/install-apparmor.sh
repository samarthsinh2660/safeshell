#!/usr/bin/env bash
# Install (or remove) the AppArmor profile that lets one `safeshell` binary use user
# namespaces. Needs sudo. It shows the profile and asks before changing anything.
#
#   scripts/install-apparmor.sh [path/to/safeshell]              install (default: bin/safeshell)
#   scripts/install-apparmor.sh --uninstall [path/to/safeshell]  remove that binary's profile
#   add --yes to skip the confirmation question
#
# Each binary gets its own profile file, safeshell-ss-<12 hex of the sha256 of its path>, so
# several installs side by side do not replace each other. A file that belongs to a package
# (the .deb's safeshell-ss for /usr/bin/safeshell) is never overwritten or deleted.
# SAFESHELL_APPARMOR_DIR (default /etc/apparmor.d) exists so tests can use a fake folder.
set -euo pipefail

dir=${SAFESHELL_APPARMOR_DIR:-/etc/apparmor.d}
here=$(cd "$(dirname "$0")/.." && pwd)
yes=0 uninstall=0 bin=""
for a in "$@"; do
  case "$a" in
    --yes) yes=1 ;;
    --uninstall) uninstall=1 ;;
    -*) echo "unknown option $a" >&2; exit 2 ;;
    *) bin="$a" ;;
  esac
done

confirm() { [ "$yes" = 1 ] && return 0; read -r -p "$1 [y/N] " r; [ "$r" = y ] || [ "$r" = Y ]; }
owned_by_package() { dpkg -S "$1" >/dev/null 2>&1; }

bin=$(realpath "${bin:-$here/bin/safeshell}")
case "$bin" in *[!A-Za-z0-9/_.-]*) echo "path has characters not allowed in the profile: $bin" >&2; exit 1 ;; esac
name=safeshell-ss-$(printf %s "$bin" | sha256sum | cut -c1-12)
profile_file=$dir/$name

# Older versions of this script wrote every binary's profile to safeshell-ss. If that file is
# not from a package and was written for this binary, this script replaces it with the new name.
legacy=$dir/safeshell-ss
legacy_is_ours=0
if [ -f "$legacy" ] && ! owned_by_package "$legacy" && grep -q "^profile safeshell-ss $bin " "$legacy"; then
  legacy_is_ours=1
fi

remove_legacy() {
  echo "Replacing the old profile $legacy (written by an older version of this script for this binary)."
  sudo apparmor_parser -R "$legacy" 2>/dev/null || true
  sudo rm -f "$legacy"
}

if [ "$uninstall" = 1 ]; then
  if [ ! -f "$profile_file" ] && [ "$legacy_is_ours" = 0 ]; then
    echo "no profile from this script for $bin (nothing to remove)"
    exit 0
  fi
  if [ -f "$profile_file" ] && owned_by_package "$profile_file"; then
    echo "$profile_file belongs to a package; remove it with your package manager" >&2
    exit 1
  fi
  confirm "Remove the profile for $bin?" || exit 1
  if [ -f "$profile_file" ]; then
    sudo apparmor_parser -R "$profile_file" 2>/dev/null || true
    sudo rm -f "$profile_file"
  fi
  [ "$legacy_is_ours" = 1 ] && remove_legacy
  echo "removed"
  exit 0
fi

[ -x "$bin" ] || { echo "no executable at $bin (run: make build)" >&2; exit 1; }
if [ "$bin" = /usr/bin/safeshell ] && [ -f "$dir/safeshell-ss" ] && owned_by_package "$dir/safeshell-ss"; then
  echo "The package already installed the AppArmor rule for $bin ($dir/safeshell-ss); nothing to do." >&2
  exit 1
fi
if [ -f "$profile_file" ] && owned_by_package "$profile_file"; then
  echo "$profile_file belongs to a package, so it will not be replaced." >&2
  exit 1
fi

profile=$(sed "s#@NAME@#$name#g; s#@BIN@#$bin#" "$here/packaging/apparmor/safeshell-ss.tmpl")
echo "This profile will be written to $profile_file:"
echo
echo "$profile"
echo
echo "It applies only to: $bin"
echo "Anyone who can replace that file gets the same permission, so keep it in a folder only you can write to."
[ "$legacy_is_ours" = 1 ] && echo "The old profile $legacy for this binary will be removed."
confirm "Install it?" || exit 1

# The old profile goes first: two loaded profiles for one binary would both claim it.
if [ "$legacy_is_ours" = 1 ]; then remove_legacy; fi
echo "$profile" | sudo tee "$profile_file" >/dev/null
sudo apparmor_parser -r "$profile_file"
echo "installed. Check with: $bin doctor"
