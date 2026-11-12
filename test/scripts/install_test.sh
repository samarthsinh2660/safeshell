#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/scripts" "$fixture/home"
cp "$repo/scripts/install.sh" "$fixture/scripts/"
# install.sh installs a prebuilt safeshell found next to scripts/, so the stub goes there.
cat >"$fixture/safeshell" <<'STUB'
#!/usr/bin/env bash
[[ "$1" != doctor ]]
STUB
cat >"$fixture/scripts/install-apparmor.sh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$APPARMOR_ARGS"
STUB
chmod +x "$fixture/safeshell" "$fixture/scripts/install-apparmor.sh"
dest="$fixture/home/.local/bin/safeshell"
printf 'y\n' | env HOME="$fixture/home" APPARMOR_ARGS="$fixture/args" bash "$fixture/scripts/install.sh" >/dev/null
diff -u <(printf '%s\n' "$dest") "$fixture/args"
env HOME="$fixture/home" APPARMOR_ARGS="$fixture/args" bash "$fixture/scripts/install.sh" --yes </dev/null >/dev/null
diff -u <(printf '%s\n' "$dest" --yes) "$fixture/args"
