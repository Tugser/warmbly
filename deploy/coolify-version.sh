#!/bin/sh
# Generate an ephemeral Compose override with the identity of this checkout.
set -eu

commit=$(git rev-parse --verify HEAD)
case "$commit" in
  *[!0-9a-f]*|'') echo 'Invalid Git commit' >&2; exit 1 ;;
esac

# Coolify uses a shallow fork checkout without upstream release tags. Restore
# history before fetching tags so git describe counts the correct ancestry.
version=
if { [ "$(git rev-parse --is-shallow-repository)" = false ] || git fetch -q --unshallow origin; } &&
   git fetch -q --tags https://github.com/warmbly/warmbly.git; then
  candidate=$(git describe --tags --match 'v[0-9]*' --abbrev=7 2>/dev/null || true)
  if printf '%s\n' "$candidate" | grep -Eq '^v[0-9]+\.[0-9]+(\.[0-9]+)?(-[A-Za-z0-9.-]+)?$'; then
    version=$candidate
  fi
fi
if [ -z "$version" ]; then
  version=$(git rev-parse --short=12 HEAD)
  echo 'Release tag unavailable; using the source commit as version.' >&2
fi

printf 'services:\n  backend:\n    build:\n      args:\n        VERSION: "%s"\n        COMMIT: "%s"\n' \
  "$version" "$commit" > deploy/docker-compose.version.override.yml
printf 'Warmbly build identity: %s (%s)\n' "$version" "$commit"
