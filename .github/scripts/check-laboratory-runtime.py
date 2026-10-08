#!/usr/bin/env python3
"""Check an actual GetFeatures response captured from the selected runtime."""
import json
from pathlib import Path
import sys

if len(sys.argv) != 2:
    raise SystemExit('usage: check-laboratory-runtime.py ACTUAL_FEATURES_JSON')
features = json.loads(Path(sys.argv[1]).read_text())
# These are protobuf's actual JSON spellings; snake_case is accepted as protobuf
# JSON also permits the original proto field names. Missing is never support.
life = features.get('lifecycle', {})
for camel, snake in [('perLabStop', 'per_lab_stop'), ('confirmedRuntime', 'confirmed_runtime')]:
    if life.get(camel, life.get(snake)) is not True:
        raise SystemExit('runtime lifecycle support unavailable: ' + camel)
print('Actual selected runtime advertises per-Lab stop and confirmed runtime')
