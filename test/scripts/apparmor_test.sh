#!/usr/bin/env bash
# Runs scripts/install-apparmor.sh against a fake profile folder with fake dpkg, sudo and
# apparmor_parser, and checks that the .deb's profile and per-binary script profiles coexist.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
t=$(mktemp -d)
trap 'rm -rf -- "$t"' EXIT
mkdir -p "$t/fakebin" "$t/etc" "$t/home/.local/bin" "$t/other"
cat >"$t/fakebin/sudo" <<'STUB'
#!/usr/bin/env bash
exec "$@"
STUB
cat >"$t/fakebin/apparmor_parser" <<'STUB'
#!/usr/bin/env bash
echo "$@" >>"$FAKE_LOG"
STUB
cat >"$t/fakebin/dpkg" <<'STUB'
#!/usr/bin/env bash
[ "$1" = -S ] && grep -qxF -- "$2" "$FAKE_OWNED"
STUB
chmod +x "$t/fakebin"/*
for b in "$t/home/.local/bin/safeshell" "$t/other/safeshell"; do printf '#!/bin/sh\n' >"$b"; chmod +x "$b"; done
export PATH="$t/fakebin:$PATH" SAFESHELL_APPARMOR_DIR="$t/etc" FAKE_LOG="$t/log" FAKE_OWNED="$t/owned"
: >"$FAKE_OWNED"
run() { bash "$repo/scripts/install-apparmor.sh" --yes "$@" >"$t/out" 2>&1; }
fail() { echo "FAIL: $1"; cat "$t/out"; exit 1; }
count() { find "$t/etc" -type f | wc -l; }
tmpl() { sed "s#@NAME@#safeshell-ss#; s#@BIN@#$1#" "$repo/packaging/apparmor/safeshell-ss.tmpl"; }

# The .deb's profile, owned by a package.
tmpl /usr/bin/safeshell >"$t/etc/safeshell-ss"
echo "$t/etc/safeshell-ss" >"$FAKE_OWNED"
deb=$(cat "$t/etc/safeshell-ss")

run "$t/home/.local/bin/safeshell" || fail "install for ~/.local/bin"
run "$t/other/safeshell" || fail "install for a second binary"
[ "$(count)" = 3 ] || fail "want deb profile plus two script profiles"
[ "$(cat "$t/etc/safeshell-ss")" = "$deb" ] || fail "deb profile changed"
a=$(grep -l "$t/home/.local/bin/safeshell" "$t"/etc/safeshell-ss-*)
grep -q "^profile $(basename "$a") $t/home/.local/bin/safeshell " "$a" || fail "profile name is not per binary"
b=$(grep -l "$t/other/safeshell" "$t"/etc/safeshell-ss-*)
[ "$a" != "$b" ] || fail "two binaries share a profile"

run --uninstall "$t/home/.local/bin/safeshell" || fail "uninstall"
{ [ ! -e "$a" ] && [ -e "$b" ] && [ "$(cat "$t/etc/safeshell-ss")" = "$deb" ]; } || fail "uninstall removed more than one profile"
run --uninstall "$t/home/.local/bin/safeshell" || fail "uninstall twice"
grep -q "nothing to remove" "$t/out" || fail "second uninstall should say nothing to remove"

# A package-owned file for the same binary is never replaced or removed.
echo "$a" >>"$FAKE_OWNED"
echo owned >"$a"
if run "$t/home/.local/bin/safeshell"; then fail "overwrote a package-owned profile"; fi
if run --uninstall "$t/home/.local/bin/safeshell"; then fail "removed a package-owned profile"; fi
[ "$(cat "$a")" = owned ] || fail "package-owned profile changed"
rm "$a"
echo "$t/etc/safeshell-ss" >"$FAKE_OWNED"

# The package already covers /usr/bin/safeshell.
if [ -x /usr/bin/safeshell ]; then
  if run /usr/bin/safeshell; then fail "installed over the package for /usr/bin/safeshell"; fi
  grep -q "nothing to do" "$t/out" || fail "no explanation for /usr/bin/safeshell"
fi

# Migration: the old script's safeshell-ss for this binary is replaced; one for another path is left.
rm "$t/etc/safeshell-ss"
: >"$FAKE_OWNED"
tmpl "$t/home/.local/bin/safeshell" >"$t/etc/safeshell-ss"
run "$t/home/.local/bin/safeshell" || fail "migration install"
{ [ ! -e "$t/etc/safeshell-ss" ] && [ -e "$a" ]; } || fail "old profile not replaced"
grep -q "old profile" "$t/out" || fail "migration not explained"
tmpl "$t/other/safeshell" >"$t/etc/safeshell-ss"
run --uninstall "$t/home/.local/bin/safeshell" || fail "uninstall after migration"
[ -e "$t/etc/safeshell-ss" ] || fail "removed an old profile written for another binary"
run --uninstall "$t/other/safeshell" || fail "uninstall old-style profile"
[ ! -e "$t/etc/safeshell-ss" ] || fail "old-style profile for this binary not removed"
echo ok
