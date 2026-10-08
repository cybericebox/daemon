#!/usr/bin/env python3
"""Copy reviewed committed source, preserving bytes and recorded generators."""
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys

if len(sys.argv) != 3:
    raise SystemExit('usage: sync-laboratory-sdk.py SOURCE_PATH SOURCE_COMMIT')
sys.dont_write_bytecode = True
source = Path(sys.argv[1]).resolve()
revision = sys.argv[2]
if not source.is_dir():
    raise SystemExit('Laboratory snapshot source does not exist')
if subprocess.check_output(['git', '-C', str(source), 'status', '--porcelain']).strip():
    raise SystemExit('Laboratory snapshot source must be clean')
# Symbolic revisions are refused: synchronization must name an immutable commit.
if not re.fullmatch(r'[0-9a-f]{7,40}', revision):
    raise SystemExit('source revision must be an explicit commit SHA')
commit = subprocess.check_output(['git', '-C', str(source), 'rev-parse', '--verify', revision + '^{commit}']).decode().strip()
spec = importlib.util.spec_from_file_location('sdk_checker', Path(__file__).with_name('check-laboratory-sdk.py'))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)
dest = Path(__file__).resolve().parents[2] / 'third_party/laboratory-sdk'
hashes = {}
for name in checker.SOURCE_FILES:
    data = subprocess.check_output(['git', '-C', str(source), 'show', commit + ':' + name])
    target = dest / name
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)
    hashes[name] = hashlib.sha256(data).hexdigest()
(dest / 'go.mod').write_text('''module github.com/cybericebox/laboratory

go 1.27.0

require (
 google.golang.org/grpc v1.84.0
 google.golang.org/protobuf v1.36.12
 google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc
)

require (
 golang.org/x/net v0.59.0 // indirect
 golang.org/x/sys v0.48.0 // indirect
 golang.org/x/text v0.42.0 // indirect
)
''')
# The selected packages use grpc/protobuf and the same existing network module
# family as the daemon. Preserve producer checksum bytes for that scoped closure.
allowed_sums = {'google.golang.org/grpc', 'google.golang.org/protobuf',
                'google.golang.org/genproto/googleapis/rpc', 'golang.org/x/net',
                'golang.org/x/sys', 'golang.org/x/text', 'github.com/google/go-cmp'}
source_sums = subprocess.check_output(['git', '-C', str(source), 'show', commit + ':go.sum']).decode()
(dest / 'go.sum').write_text(''.join(line + '\n' for line in source_sums.splitlines() if line.split()[0] in allowed_sums))
(dest / 'SOURCE_COMMIT').write_text(commit + '\n')
(dest / 'README.md').write_text('''# Laboratory SDK source snapshot

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
''')
generator = {}
for name in ('agent.pb.go', 'agent_grpc.pb.go'):
    for line in (dest / 'pkg/agent/protobuf' / name).read_text().splitlines()[:12]:
        match = re.search(r'(protoc(?:-gen-go(?:-grpc)?)?)\s+(v[0-9.]+)', line)
        if match:
            prior = generator.get(match[1])
            if prior is not None and prior != match[2]:
                raise SystemExit('inconsistent protoc generated banners')
            generator[match[1]] = match[2]
bundle = {name: hashlib.sha256((dest / name).read_bytes()).hexdigest() for name in checker.BUNDLE_FILES}
provenance = {'Repository': 'https://github.com/cybericebox/laboratory', 'SourceCommit': commit,
              'Files': hashes, 'BundleFiles': bundle, 'Generator': generator,
              'GeneratorRecipe': 'protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative pkg/agent/protobuf/agent.proto',
              'CopiedAt': datetime.now(timezone.utc).isoformat()}
(dest / 'PROVENANCE.json').write_text(json.dumps(provenance, indent=2) + '\n')
(dest / 'SHA256SUMS').write_text(''.join(digest + '  ' + name + '\n' for name, digest in sorted((hashes | bundle).items())))
# Verification is deliberately a separate command, so code-only synchronization
# does not silently run any final checks before the owner's integration gate.
dockerfile = dest.parents[1] / 'Dockerfile'
docker_text = dockerfile.read_text()
docker_text, count = re.subn(r'^ARG LABORATORY_CONTRACT_SOURCE=[^\n]*$', 'ARG LABORATORY_CONTRACT_SOURCE=' + commit, docker_text, count=1, flags=re.M)
if count != 1:
    raise SystemExit('Docker SDK source argument missing')
dockerfile.write_text(docker_text)
print('Copied Laboratory SDK source commit ' + commit)
