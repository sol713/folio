#!/usr/bin/env python3
"""Check checked-in dependency inventory coverage and unchanged license bytes."""
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1] / 'third_party/licenses'
inventory = json.loads((root / 'modules.json').read_text())
coverage = json.loads((root / 'coverage.json').read_text())
expected = {(m['Path'], m['Version']) for m in inventory if m['Path'] != 'folio'}
actual = {(m['path'], m['version']) for m in coverage['modules']}
assert expected == actual, 'inventory and coverage modules differ'
assert len(actual) == len(coverage['modules']), 'duplicate coverage module'
files = []
for module in coverage['modules']:
    assert any(Path(f['file']).name.startswith('LICENSE') for f in module['license_files']), 'module lacks a license'
    expected_directory = module['path'].replace('/', '__') + '@' + module['version']
    assert all(Path(f['file']).parts[0] == expected_directory for f in module['license_files']), 'license is attached to the wrong module'
    files.extend(module['license_files'])
files.extend(coverage['toolchain_license']['files'])
for item in files:
    relative = Path(item['file'])
    assert not relative.is_absolute() and '..' not in relative.parts, 'unsafe license path'
    source = root / relative
    assert source.is_file() and not source.is_symlink(), 'license file missing or linked'
    assert hashlib.sha256(source.read_bytes()).hexdigest() == item['sha256'], 'license bytes changed'
print(json.dumps({'upstream_modules': len(actual), 'license_files': len(files), 'status': 'passed'}))
