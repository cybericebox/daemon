#!/usr/bin/env bash
# Runs before `make dev-push`: the laboratory pin of go.mod.
#   LAB absent  go.mod is left as it is
#   LAB=        pin the commit ../laboratory is on
#   LAB=<x>     pin <x> (a commit, a branch or vX.Y.Z)
# The pin must be on GitHub (laboratory develop, or a release tag); ../laboratory must have no uncommitted changes.
# LAB_SET=1 tells that LAB was given (the Makefile sets it).
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }
repo=cybericebox/laboratory
local_lab=../laboratory

pinned() { awk -v m="github.com/$repo" '$1 == m { print $2; exit } $1 == "require" && $2 == m { print $3; exit }' go.mod; }

# the commit (hex prefix) of the current pin, or empty when it is not known locally
pin_commit() {
  local v=$1
  if [[ "$v" =~ -([0-9a-f]{12})$ ]]; then
    echo "${BASH_REMATCH[1]}"
  elif [[ "$v" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && -d "$local_lab/.git" ]]; then
    git -C "$local_lab" rev-parse -q --verify "$v^{commit}" 2>/dev/null || true
  fi
}

warn_mismatch() {
  [[ -d "$local_lab/.git" ]] || return 0
  local head pin
  head=$(git -C "$local_lab" rev-parse HEAD)
  pin=$(pin_commit "$(pinned)")
  if [[ -z "$pin" ]]; then
    echo "warning: cannot tell which laboratory commit go.mod pins ($(pinned)); local $local_lab is on ${head:0:12}" >&2
  elif [[ "$head" != "$pin"* && "$pin" != "$head"* ]]; then
    echo "warning: go.mod pins laboratory ${pin:0:12}, but local $local_lab is on ${head:0:12}; CI builds the pin, not your local copy" >&2
  fi
}

if [[ "${LAB_SET:-}" != 1 ]]; then
  warn_mismatch
  exit 0
fi

target=${LAB:-}
if [[ -z "$target" ]]; then
  [[ -d "$local_lab/.git" ]] || die "$local_lab is not a git checkout"
  [[ -z "$(git -C "$local_lab" status --porcelain --untracked-files=no)" ]] ||
    die "$local_lab has uncommitted changes; commit them and run make dev-push in laboratory first"
  target=$(git -C "$local_lab" rev-parse HEAD)
fi

if [[ "$target" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  gh api "repos/$repo/git/ref/tags/$target" --silent 2>/dev/null || die "laboratory release $target does not exist on GitHub"
else
  sha=$(gh api "repos/$repo/commits/$target" --jq .sha 2>/dev/null) ||
    die "$target is not on GitHub; run make dev-push in laboratory first"
  status=$(gh api "repos/$repo/compare/develop...$sha" --jq .status 2>/dev/null || true)
  case "$status" in
    identical | behind) ;;
    *) die "laboratory $sha is not in laboratory develop yet; run make dev-push in laboratory and wait for the merge" ;;
  esac
  target=$sha
fi

echo "pinning laboratory $target"
GOWORK=off GOPRIVATE="github.com/$repo" go get "github.com/$repo@$target"
GOWORK=off GOPRIVATE="github.com/$repo" go mod tidy
GOWORK=off go build ./... || die "the daemon does not build against laboratory $target"
if [[ -n "$(git status --porcelain go.mod go.sum)" ]]; then
  git commit -m "Pin laboratory $(pinned)" go.mod go.sum
fi
warn_mismatch
