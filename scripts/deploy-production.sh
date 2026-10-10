#!/usr/bin/env bash
set -euo pipefail

tag=${1:?usage: deploy-production.sh <git-sha>}
if [[ ! $tag =~ ^[0-9a-f]{40}$ ]]; then
  echo "Expected a 40-character commit SHA" >&2
  exit 2
fi

cd "$(dirname "$0")/.."
test -f .env.production || { echo "Missing .env.production" >&2; exit 2; }
if grep -q 'replace-with' .env.production; then
  echo "Replace the sample database password in .env.production" >&2
  exit 2
fi
old_tag=''
if [[ -f .env.release ]]; then
  old_tag=$(sed -n 's/^PERLINADV_IMAGE_TAG=//p' .env.release)
fi
printf 'PERLINADV_IMAGE_TAG=%s\n' "$tag" > .env.release.next
mv .env.release.next .env.release

compose() {
  docker compose --env-file .env.production --env-file .env.release -f compose.production.yml "$@"
}

rollback() {
  if [[ $old_tag =~ ^[0-9a-f]{40}$ ]]; then
    echo "Restoring previous image $old_tag" >&2
    printf 'PERLINADV_IMAGE_TAG=%s\n' "$old_tag" > .env.release.next
    mv .env.release.next .env.release
    compose up -d --no-deps api worker || true
  fi
}

if ! compose --profile tools pull api worker migrate; then
  rollback
  exit 1
fi
if ! compose up -d --wait db; then
  rollback
  exit 1
fi
if ! compose --profile tools run --rm migrate; then
  rollback
  exit 1
fi
if ! compose up -d --no-deps --wait api worker; then
  rollback
  exit 1
fi
compose ps
