#!/bin/sh
# Install the AppArmor profile Bubblewrap needs for Codex on Ubuntu 24.04.
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this setup step as root to install the Bubblewrap AppArmor profile." >&2
  exit 1
fi

command -v apparmor_parser >/dev/null
test -x /usr/bin/bwrap
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
profile=/etc/apparmor.d/bwrap
if [ -e "$profile" ] && ! cmp -s "$profile" "$script_dir/linux/bwrap"; then
  echo "$profile already exists with local changes. Load the locally managed profile instead." >&2
  exit 1
fi

install -m 0644 "$script_dir/linux/bwrap" "$profile"
apparmor_parser -r "$profile"
