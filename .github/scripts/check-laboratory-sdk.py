#!/usr/bin/env python3
"""Validate the exact scoped Laboratory source snapshot without sibling checkout."""
import argparse
import hashlib
import json
from pathlib import Path
import re

SOURCE_FILES = (
    'LICENSE', 'NOTICE', 'pkg/agent/protobuf/agent.proto',
    'pkg/agent/protobuf/agent.pb.go', 'pkg/agent/protobuf/agent_grpc.pb.go',
    'pkg/agent/client/client.go', 'pkg/agent/client/count.go',
    'pkg/agent/client/terminating.go', 'pkg/agent/client/client_test.go',
    'pkg/tlsreload/tlsreload.go', 'pkg/tlsreload/tlsreload_test.go',
    'pkg/vpnprobe/probe.go', 'pkg/vpnprobe/probe_test.go',
)
BUNDLE_FILES = ('go.mod', 'go.sum', 'SOURCE_COMMIT', 'README.md')
TAGS = {
    'LabGroup': {'uid': 14, 'generation': 15, 'vpn_size': 16, 'gateway_size': 17, 'lifecycle': 18},
    'LabGroupStatus': {'lifecycle': 9, 'resources': 10, 'current_vpn_boot_id': 11, 'current_vpn_boot_available': 12, 'current_vpn_boot_observed_unix_ms': 13, 'retirement': 14},
    'Lab': {'uid': 13, 'generation': 14},
    'LabStatus': {'lifecycle': 41, 'resources': 42, 'retirement': 43},
    'ResourceAllocation': {'configured_requests': 1, 'configured_limits': 2, 'allocated_requests': 3, 'runtime_state': 4, 'observed_unix_ms': 5, 'released_unix_ms': 6, 'snapshot_quota_bytes': 9, 'storage_state': 10, 'physical_storage_bytes_available': 11, 'physical_storage_bytes': 12, 'operation_id': 13, 'lifecycle_revision': 14},
    'LifecycleFeature': {'per_lab_stop': 1, 'required_snapshot': 2, 'confirmed_runtime': 3, 'retained_restart': 4, 'full_group_stop': 5},
    'GroupPodsFeature': {'sizing_v2': 5},
    'LabRetirementTarget': {'stop_target': 1, 'retirement_operation_id': 2, 'retirement_revision': 3},
    'GroupRetirementTarget': {'stop_target': 1, 'retirement_operation_id': 2, 'retirement_revision': 3},
    'RetirementStatus': {'expected_uid': 1, 'stop_operation_id': 2, 'stop_revision': 3, 'operation_id': 4, 'revision': 5, 'observed_generation': 6, 'state': 7, 'observed_unix_ms': 8, 'runtime_absent': 9, 'storage_state': 10, 'cleanup_complete': 11, 'physical_storage_bytes_available': 12, 'physical_storage_bytes': 13, 'error': 14, 'requested_unix_ms': 15},
}


def check(root):
    manifest = json.loads((root / 'PROVENANCE.json').read_text())
    if manifest['Repository'] != 'https://github.com/cybericebox/laboratory':
        raise ValueError('unexpected Laboratory repository')
    commit = manifest['SourceCommit']
    if not re.fullmatch(r'[0-9a-f]{40}', commit) or commit == '0' * 40:
        raise ValueError('invalid Laboratory source commit')
    if (root / 'SOURCE_COMMIT').read_text().strip() != commit:
        raise ValueError('source commit does not match bundled pin')
    if set(manifest['Files']) != set(SOURCE_FILES) or set(manifest['BundleFiles']) != set(BUNDLE_FILES):
        raise ValueError('SDK scope differs from approved package list')
    files = manifest['Files'] | manifest['BundleFiles']
    for name, expected in files.items():
        if not re.fullmatch(r'[0-9a-f]{64}', expected):
            raise ValueError('invalid SHA256 for ' + name)
        if hashlib.sha256((root / name).read_bytes()).hexdigest() != expected:
            raise ValueError('SDK content changed: ' + name)
    sums = ''.join(digest + '  ' + name + '\n' for name, digest in sorted(files.items()))
    if (root / 'SHA256SUMS').read_text() != sums:
        raise ValueError('SHA256SUMS differs from provenance')
    if set(manifest['Generator']) != {'protoc', 'protoc-gen-go', 'protoc-gen-go-grpc'}:
        raise ValueError('generated toolchain provenance incomplete')
    for tool, version in manifest['Generator'].items():
        if not re.fullmatch(r'v[0-9]+(?:\.[0-9]+)+', version):
            raise ValueError('invalid generator version: ' + tool)
    pb = (root / 'pkg/agent/protobuf/agent.pb.go').read_text()
    grpc = (root / 'pkg/agent/protobuf/agent_grpc.pb.go').read_text()
    for tool, content in [('protoc-gen-go', pb), ('protoc-gen-go-grpc', grpc), ('protoc', pb), ('protoc', grpc)]:
        if not re.search(r'//\s*' + re.escape(tool) + r'\s+' + re.escape(manifest['Generator'][tool]) + r'\s*$', content, re.M):
            raise ValueError('generator banner mismatch: ' + tool)
    proto = (root / 'pkg/agent/protobuf/agent.proto').read_text()
    client = (root / 'pkg/agent/client/client.go').read_text()
    for method in ('StopLabs', 'StartLabs', 'StopLabGroups', 'StartLabGroups', 'RetireLabs', 'RetireLabGroups'):
        if not re.search(r'rpc\s+' + method + r'\s*\(', proto) or ('LabManager_' + method + '_FullMethodName') not in grpc or not re.search(r'\b' + method + r'Request\s*=\s*protobuf\.' + method + r'Request', client) or 'protobuf.LabManagerClient' not in client:
            raise ValueError('missing lifecycle RPC: ' + method)
    for message, fields in TAGS.items():
        body = re.search(r'message\s+' + message + r'\s*\{([^}]+)\}', proto, re.S)
        if body is None:
            raise ValueError('missing message: ' + message)
        for field, tag in fields.items():
            if not re.search(r'\b' + field + r'\s*=\s*' + str(tag) + r'\s*;', body[1]):
                raise ValueError('incorrect wire tag: ' + message + '.' + field)
    return commit


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path, default=Path(__file__).resolve().parents[2] / 'third_party/laboratory-sdk')
    args = parser.parse_args()
    try:
        commit = check(args.bundle)
    except (OSError, ValueError, KeyError) as exc:
        raise SystemExit('Laboratory SDK verification failed: ' + str(exc))
    print('Laboratory SDK content/protocol provenance: ' + commit)
