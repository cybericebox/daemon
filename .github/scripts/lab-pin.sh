#!/usr/bin/env bash
# The laboratory pin of the daemon (go.mod is the only source of it).
#   lab-pin.sh version         the pinned laboratory version
#   lab-pin.sh check-label IMAGE TAG  fail unless the image carries the pinned version in org.cybericebox.laboratory.commit
#   lab-pin.sh require-release fail unless the pin is a laboratory release (vX.Y.Z) that exists on GitHub
set -euo pipefail

mod=cybericebox/laboratory
version=$(awk -v m="github.com/$mod" '$1 == m { print $2; exit } $1 == "require" && $2 == m { print $3; exit }' go.mod)
[[ -n "$version" ]] || { echo "error: go.mod does not require github.com/$mod" >&2; exit 1; }

case "${1:-}" in
  version) echo "$version" ;;
  check-label)
    ref="$2:$3"
    labels=$(docker buildx imagetools inspect "$ref" --format '{{json .Image}}' |
      jq -r '[.. | objects | .Labels? // empty | .["org.cybericebox.laboratory.commit"]? // empty] | unique | join(",")')
    if [[ "$labels" != "$version" ]]; then
      echo "::error::$ref carries the laboratory label '${labels:-none}', go.mod pins $version"
      exit 1
    fi
    echo "$ref carries the laboratory label $version"
    ;;
  require-release)
    if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || "$version" == v0.0.0 ]]; then
      echo "::error::go.mod pins laboratory $version, which is not a release; pin a release first (make dev-push LAB=vX.Y.Z)"
      exit 1
    fi
    git ls-remote --exit-code --tags "https://github.com/$mod" "refs/tags/$version" >/dev/null ||
      { echo "::error::laboratory release $version does not exist"; exit 1; }
    echo "laboratory pin: release $version"
    ;;
  *) echo "usage: lab-pin.sh version|check-label|require-release" >&2; exit 1 ;;
esac
