#!/usr/bin/env bash
# Checks the contents of built .deb files. Usage: deb_test.sh dist/*.deb
set -uo pipefail
[ $# -gt 0 ] || { echo "usage: $0 FILE.deb..."; exit 2; }
fail=0
for deb in "$@"; do
  files=$(dpkg-deb -c "$deb")
  info=$(dpkg-deb -I "$deb" control)
  conffiles=$(dpkg-deb -I "$deb" conffiles 2>/dev/null)
  want() { # description, text, pattern
    grep -q -- "$3" <<<"$2" || { echo "FAIL $deb: $1"; fail=1; }
  }
  want "no /usr/bin/safeshell" "$files" ' \./usr/bin/safeshell$'
  want "no man page" "$files" ' \./usr/share/man/man1/safeshell\.1\.gz$'
  want "no copyright file" "$files" ' \./usr/share/doc/safeshell/copyright$'
  want "no AppArmor profile" "$files" ' \./etc/apparmor\.d/safeshell-ss$'
  want "AppArmor profile is not a conffile" "$conffiles" '^/etc/apparmor\.d/safeshell-ss$'
  want "no Recommends: apparmor" "$info" '^Recommends: apparmor$'
  if grep -q 'example\.invalid' <<<"$info"; then echo "FAIL $deb: placeholder maintainer"; fail=1; fi
done
[ "$fail" = 0 ] && echo "ok: $*"
exit "$fail"
