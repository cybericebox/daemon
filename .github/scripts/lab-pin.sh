#!/usr/bin/env bash
# Runtime distribution pin and checked-in SDK source identity are distinct.
#   lab-pin.sh version         required runtime distribution version
#   lab-pin.sh contract-source exact bundled SDK source commit
#   lab-pin.sh check-runtime-features JSON  require actual native lifecycle support
#   lab-pin.sh check-label IMAGE TAG  fail unless the image carries the pinned version in org.cybericebox.laboratory.commit
#   lab-pin.sh require-release fail unless the pin is a laboratory release (vX.Y.Z) that exists on GitHub
set -euo pipefail

mod=cybericebox/laboratory
version=$(awk -v m="github.com/$mod" '$1 == m { print $2; exit } $1 == "require" && $2 == m { print $3; exit }' go.mod)
[[ -n "$version" ]] || { echo "error: go.mod does not require github.com/$mod" >&2; exit 1; }

snapshot_selected() {
  awk '$1 == "replace" && $2 == "github.com/cybericebox/laboratory" && $3 == "=>" && $4 == "./third_party/laboratory-sdk" { found=1 } END {exit !found}' go.mod
}
contract_source() {
  if snapshot_selected; then
    python3 .github/scripts/check-laboratory-sdk.py >/dev/null
    python3 -c 'import json; print(json.load(open("third_party/laboratory-sdk/PROVENANCE.json"))["SourceCommit"])'
  else
    # Published distribution without a scoped source snapshot uses its explicit pin.
    echo "$version"
  fi
}

case "${1:-}" in
  version) echo "$version" ;;
  contract-source) contract_source ;;
  check-runtime-features)
    python3 .github/scripts/check-laboratory-runtime.py "$2"
    ;;
  check-label)
    ref="$2:$3"
    inspection=$(docker buildx imagetools inspect "$ref" --format '{{json .Image}}')
    labels=$(jq -r '[.. | objects | .Labels? // empty | .["org.cybericebox.laboratory.commit"]? // empty] | unique | join(",")' <<<"$inspection")
    source=$(jq -r '[.. | objects | .Labels? // empty | .["org.cybericebox.laboratory.contract.source"]? // empty] | unique | join(",")' <<<"$inspection")
    expected_source=$(contract_source)
    if [[ "$source" != "$expected_source" ]]; then
      echo "::error::$ref carries SDK source '${source:-none}', expected $expected_source"
      exit 1
    fi
    if [[ "$labels" != "$version" ]]; then
      echo "::error::$ref carries the laboratory label '${labels:-none}', go.mod pins $version"
      exit 1
    fi
    echo "$ref carries the laboratory label $version"
    ;;
  require-release)
    if snapshot_selected; then
      echo "::error::SDK snapshot selected; release requires an owner-approved runtime and contract distribution"
      exit 1
    fi
    if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || "$version" == v0.0.0 ]]; then
      echo "::error::go.mod pins laboratory $version, which is not a release; pin a release first (make dev-push LAB=vX.Y.Z)"
      exit 1
    fi
    git ls-remote --exit-code --tags "https://github.com/$mod" "refs/tags/$version" >/dev/null ||
      { echo "::error::laboratory release $version does not exist"; exit 1; }
    echo "laboratory pin: release $version"
    ;;
  *) echo "usage: lab-pin.sh version|contract-source|check-runtime-features|check-label|require-release" >&2; exit 1 ;;
esac
