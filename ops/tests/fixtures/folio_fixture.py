"""Synthetic protocol fixture, not a blog implementation or production binary."""
import hashlib
import http.server
import json
import os
from pathlib import Path
import secrets
import signal
import sqlite3
import sys

STATE = {"schema": 1, "revision": 1, "audit": [], "posts": [{"title": "fixture"}], "media": [{"data": "Zml4dHVyZQ=="}]}
HASH = hashlib.sha256(json.dumps(STATE, sort_keys=True).encode()).hexdigest()
BACKUP = {"format": "folio-backup", "version": 1, "created_at": "2026-10-03T00:00:00Z", "sha256": HASH, "state": STATE}
RESTORED = Path(__file__).with_name("fixture-restored.json")
argv = sys.argv[1:]
if argv[0] == "version":
    print(json.dumps({"version": "0.1.0"}))
elif argv[0] == "init":
    data = Path(argv[argv.index("--data") + 1])
    data.mkdir(mode=0o700)
    token = data / "token"
    token.write_text(secrets.token_hex(32))
    token.chmod(0o600)
    with sqlite3.connect(data / "folio.db") as conn:
        conn.execute("CREATE TABLE fixture (value TEXT)")
    print(json.dumps({"token_file": str(token)}))
elif argv[0] == "serve":
    address = argv[argv.index("--addr") + 1]
    host, port = address.split(":")
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'{"status":"ok","version":"fixture"}')
        def log_message(self, *args):
            pass
    server = http.server.ThreadingHTTPServer((host, int(port)), Handler)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    server.serve_forever()
elif argv[:2] == ["call", "backup.export"]:
    assert len(os.environ["FOLIO_TOKEN"]) >= 32
    print(json.dumps({"ok": True, "data": json.loads(RESTORED.read_text()) if RESTORED.exists() else BACKUP}))
elif argv[:2] == ["call", "backup.restore"]:
    request = json.loads(Path(argv[argv.index("--file") + 1]).read_text())
    assert request["confirm"] is True and request["backup"] == BACKUP
    restored = json.loads(json.dumps(BACKUP))
    restored["state"]["revision"] += 1
    restored["state"]["audit"].append({"id": "a" * 24, "at": "2026-10-03T00:00:00Z",
          "operation": "backup.restore", "target": "instance", "actor": "admin", "revision": 1})
    restored["sha256"] = hashlib.sha256(json.dumps(restored["state"], sort_keys=True).encode()).hexdigest()
    RESTORED.write_text(json.dumps(restored))
    print(json.dumps({"ok": True, "data": {"restored": True}}))
else:
    sys.exit(2)
