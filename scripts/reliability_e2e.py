#!/usr/bin/env python3
"""Real CLI initialization fault/retry and credential ownership regressions."""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    env = {k: v for k, v in os.environ.items() if not k.startswith('FOLIO_')}
    checks = []

    def init(directory):
        return subprocess.run([str(binary), '--lang', 'en', 'init', '--data', str(directory)],
                              env=env, capture_output=True, timeout=20)

    with tempfile.TemporaryDirectory(prefix='folio-reliability-') as tmp:
        root = Path(tmp)

        def failed_init_retry():
            data = root / 'retry'; data.mkdir()
            db = data / 'folio.db'; original = b'invalid database obstruction'
            db.write_bytes(original)
            assert init(data).returncode != 0, 'invalid database accepted'
            assert not (data / 'token').exists(), 'failed init left credentials and blocked retry'
            assert db.read_bytes() == original, 'existing database changed after failure'
            db.unlink()  # Correct only this deliberately created obstruction.
            assert init(data).returncode == 0, 'retry after correcting storage failed'
            token = (data / 'token').read_bytes()
            assert len(token.strip()) == 64, 'credential incomplete'
            assert init(data).returncode != 0, 'repeat init accepted'
            assert (data / 'token').read_bytes() == token, 'repeat init changed credentials'
            with sqlite3.connect(db) as connection:
                assert connection.execute('PRAGMA quick_check').fetchone()[0] == 'ok'

        def dangling_symlink():
            data = root / 'symlink'; data.mkdir()
            target = root / 'must-not-be-created'
            (data / 'token').symlink_to(target)
            assert init(data).returncode != 0, 'credential symlink accepted'
            assert not target.exists(), 'init followed dangling credential symlink'
            assert (data / 'token').is_symlink(), 'existing symlink replaced'

        def concurrent_init():
            data = root / 'concurrent'
            with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
                results = list(pool.map(lambda _: init(data), range(16)))
            assert sum(r.returncode == 0 for r in results) == 1, 'concurrent init has multiple/no winners'
            token = (data / 'token').read_bytes()
            assert len(token.strip()) == 64, 'winner credential incomplete'
            assert (data / 'token').stat().st_mode & 0o777 == 0o600, 'credential is not private'
            assert all(token.strip() not in r.stdout + r.stderr for r in results), 'credential logged'

        for name, check in [('init storage failure and same-directory retry', failed_init_retry),
                            ('dangling credential symlink rejected', dangling_symlink),
                            ('16 simultaneous initializers have one winner', concurrent_init)]:
            try:
                check(); checks.append({'check': name, 'ok': True})
            except AssertionError as error:
                checks.append({'check': name, 'ok': False, 'error': str(error)})
    print(json.dumps({'checks': checks, 'passed': sum(c['ok'] for c in checks),
                      'total': len(checks)}, ensure_ascii=False, indent=2))
    return 0 if all(c['ok'] for c in checks) else 1


if __name__ == '__main__':
    raise SystemExit(main())
