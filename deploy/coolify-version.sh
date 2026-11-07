#!/bin/sh
# Coolify removes .git before the build; its build env identifies the checkout.
set -eu

commit=${SOURCE_COMMIT:-}
if [ -z "$commit" ] && [ -f /artifacts/build-time.env ]; then
  commit=$(sed -n "s/^SOURCE_COMMIT=['\"]\{0,1\}\([0-9a-f]\{40\}\)['\"]\{0,1\}$/\1/p" /artifacts/build-time.env | head -n 1)
fi
if ! printf '%s\n' "$commit" | grep -Eq '^[0-9a-f]{40}$'; then
  echo 'Coolify did not provide a valid SOURCE_COMMIT.' >&2
  exit 1
fi

version=$(printf '%.12s' "$commit")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if git init -q --bare "$tmp" &&
   git -C "$tmp" fetch -q --no-tags --filter=blob:none https://github.com/Tugser/warmbly.git "$commit" &&
   git -C "$tmp" fetch -q --filter=blob:none https://github.com/warmbly/warmbly.git 'refs/tags/v*:refs/tags/v*'; then
  candidate=$(git -C "$tmp" describe --tags --match 'v[0-9]*' --abbrev=7 "$commit" 2>/dev/null || true)
  if printf '%s\n' "$candidate" | grep -Eq '^v[0-9]+\.[0-9]+(\.[0-9]+)?(-[A-Za-z0-9.-]+)?$'; then
    version=$candidate
  fi
fi

printf 'services:\n  backend:\n    build:\n      args:\n        VERSION: "%s"\n        COMMIT: "%s"\n' \
  "$version" "$commit" > deploy/docker-compose.version.override.yml
printf 'Warmbly build identity: %s (%s)\n' "$version" "$commit"
