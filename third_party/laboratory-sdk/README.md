# Laboratory SDK source snapshot

This independently buildable local module contains only pkg/agent/protobuf,
pkg/agent/client, pkg/tlsreload and pkg/vpnprobe, their original tests, LICENSE
and NOTICE. Controller, operator and native runtime implementations are excluded.
Source bytes are unchanged from the immutable producer commit in SOURCE_COMMIT.
Generated banners identify the actual toolchain. PROVENANCE.json and SHA256SUMS
record source and bundle hashes; copying is not a published Laboratory release.

From daemon root, synchronize only a reviewed, clean committed producer:

    python3 .github/scripts/sync-laboratory-sdk.py /absolute/laboratory COMMIT_SHA
    python3 .github/scripts/check-laboratory-sdk.py

The root go.mod intentionally retains require v1.0.0 and explicitly selects this
snapshot with a relative replace. GOWORK=off builds need no sibling repository.
The SDK source commit is separate from the Laboratory runtime image version.
Runtime support is discovered through its actual LifecycleFeature response; the
snapshot alone cannot establish native support. Distribution/release is blocked
until the owner approves a real runtime and contract distribution pin. Remove
replace only after that real distribution exists; never relabel v1.0.0.
