import contextlib
import copy
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import sqlite3
import struct
import subprocess
import sys
import tarfile
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import ops

OLD = "sha256:" + "a" * 64
NEW = "sha256:" + "b" * 64
TEST_TOKEN = "synthetic-test-token-" + "x" * 40


def data_fixture(root):
    root.mkdir(mode=0o700)
    (root / "token").write_text(TEST_TOKEN)
    (root / "token").chmod(0o600)
    with sqlite3.connect(root / "folio.db") as conn:
        conn.execute("CREATE TABLE test_posts (id INTEGER PRIMARY KEY, title TEXT, body BLOB)")
        conn.execute("INSERT INTO test_posts VALUES (1, ?, ?)", ("本地恢复演练", b"synthetic media bytes"))
    (root / "folio.db").chmod(0o600)
    return root


class Fixture(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="folio-ops-test-")
        self.base = Path(self.tmp.name)
        self.data = data_fixture(self.base / "data")

    def tearDown(self):
        self.tmp.cleanup()


class BackupTests(Fixture):
    def backup(self):
        path = self.base / "backup.tar.gz"
        result = ops.offline_backup(self.data, path, stopped=True)
        self.assertTrue(result["contains_token"])
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        return path

    def test_roundtrip_and_sqlite_rows(self):
        path = self.backup()
        self.assertEqual(ops.verify_offline(path)["sqlite"], "ok")
        target = self.base / "restored"
        ops.offline_restore(path, target)
        self.assertEqual(ops.sha256(target / "token"), ops.sha256(self.data / "token"))
        self.assertEqual((target / "token").stat().st_mode & 0o777, 0o600)
        self.assertEqual(target.stat().st_mode & 0o777, 0o700)
        with sqlite3.connect(target / "folio.db") as conn:
            self.assertEqual(conn.execute("SELECT title, body FROM test_posts").fetchone(),
                             ("本地恢复演练", b"synthetic media bytes"))

    def test_uncheckpointed_wal_roundtrip(self):
        conn = sqlite3.connect(self.data / "folio.db")
        try:
            conn.execute("PRAGMA journal_mode=WAL")
            conn.execute("PRAGMA wal_autocheckpoint=0")
            conn.execute("INSERT INTO test_posts VALUES (2, 'committed WAL row', X'01')")
            conn.commit()
            # This test holds an idle connection to deliberately retain a WAL file.
            path = self.backup()
            target = self.base / "restored"
            ops.offline_restore(path, target)
            with sqlite3.connect(target / "folio.db") as restored:
                self.assertEqual(restored.execute("SELECT COUNT(*) FROM test_posts").fetchone(), (2,))
        finally:
            conn.close()

    def test_stopped_proof_required(self):
        with self.assertRaises(ops.OpsError):
            ops.offline_backup(self.data, self.base / "backup.tar.gz")

    def test_existing_backup_never_overwritten(self):
        path = self.backup()
        digest = ops.sha256(path)
        with self.assertRaises(FileExistsError):
            ops.offline_backup(self.data, path, stopped=True)
        self.assertEqual(ops.sha256(path), digest)

    def test_restore_refuses_existing_target(self):
        path = self.backup()
        with self.assertRaises(ops.OpsError):
            ops.offline_restore(path, self.data)

    def test_no_backup_within_data(self):
        with self.assertRaises(ops.OpsError):
            ops.offline_backup(self.data, self.data / "backup.tar.gz", True)

    def test_symlink_in_data_refused(self):
        (self.data / "link").symlink_to(self.base / "outside")
        with self.assertRaises(ops.OpsError):
            self.backup()

    def test_special_file_refused(self):
        os.mkfifo(self.data / "fifo")
        with self.assertRaises(ops.OpsError):
            self.backup()

    def test_corrupt_sqlite_refused(self):
        (self.data / "folio.db").write_bytes(b"not a database")
        with self.assertRaises(ops.OpsError):
            self.backup()

    def hostile(self, entries):
        path = self.base / "hostile.tar.gz"
        with tarfile.open(path, "w:gz") as tar:
            for info, raw in entries:
                info.size = len(raw)
                tar.addfile(info, io.BytesIO(raw))
        return path

    def test_archive_traversal(self):
        path = self.hostile([(tarfile.TarInfo("payload/../../escape"), b"bad")])
        with self.assertRaises(ops.OpsError):
            ops.offline_restore(path, self.base / "restore")
        self.assertFalse((self.base / "escape").exists())
        self.assertFalse((self.base / "restore").exists())

    def test_archive_symlink(self):
        info = tarfile.TarInfo("payload/token")
        info.type, info.linkname = tarfile.SYMTYPE, "/etc/passwd"
        with self.assertRaises(ops.OpsError):
            ops.verify_offline(self.hostile([(info, b"")]))

    def test_archive_duplicates(self):
        with self.assertRaises(ops.OpsError):
            ops.verify_offline(self.hostile([(tarfile.TarInfo("payload/token"), b"x"),
                                           (tarfile.TarInfo("payload/token"), b"y")]))

    def test_archive_size_limit(self):
        path = self.hostile([(tarfile.TarInfo("payload/token"), b"x" * 100)])
        with patch.object(ops, "MAX_BACKUP", 16), self.assertRaises(ops.OpsError):
            ops.verify_offline(path)

    def test_hash_mismatch(self):
        good = self.backup()
        entries = []
        with tarfile.open(good, "r:gz") as tar:
            for member in tar:
                raw = tar.extractfile(member).read() if member.isfile() else b""
                if member.name == "payload/token":
                    raw = b"tampered-" + b"x" * 64
                entries.append((member, raw))
        with self.assertRaises(ops.OpsError):
            ops.verify_offline(self.hostile(entries))


class SecurityTests(Fixture):
    def test_private_write_does_not_clobber(self):
        path = self.base / "new"
        ops.write_private(path, b"first")
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        with self.assertRaises(ops.OpsError):
            ops.write_private(path, b"second")
        self.assertEqual(path.read_bytes(), b"first")

    def test_write_symlink_parent_refused(self):
        (self.base / "alias").symlink_to(self.data)
        with self.assertRaises(ops.OpsError):
            ops.write_private(self.base / "alias" / "output", b"x")

    def test_public_token_refused(self):
        (self.data / "token").chmod(0o644)
        with self.assertRaises(ops.OpsError):
            ops.token_file(self.data / "token")

    def test_short_token_refused(self):
        (self.data / "token").write_text("short")
        with self.assertRaises(ops.OpsError):
            ops.token_file(self.data / "token")

    def test_remote_plain_http_refused(self):
        for url in ["http://example.org", "http://192.168.1.10", "ftp://127.0.0.1", "http://u:p@localhost"]:
            with self.subTest(url=url), self.assertRaises(ops.OpsError):
                ops.safe_url(url)

    def test_loopback_and_tls_urls(self):
        for url in ["http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost", "https://example.org"]:
            self.assertEqual(ops.safe_url(url), url)

    def test_origin_only_urls(self):
        for url in ["http://localhost/path", "http://localhost?token=abc", "http://localhost:99999"]:
            with self.assertRaises(ops.OpsError):
                ops.safe_url(url)

    def test_compose_security_contract(self):
        doc = ops.compose_document(self.data, OLD, 8080, os.getuid(), os.getgid())
        service = doc["services"]["blog"]
        self.assertEqual(service["ports"], ["127.0.0.1:8080:8080"])
        self.assertTrue(service["read_only"])
        self.assertEqual(service["cap_drop"], ["ALL"])
        self.assertFalse(service["volumes"][0]["bind"]["create_host_path"])
        self.assertNotIn(TEST_TOKEN, json.dumps(doc))
        self.assertEqual(service["healthcheck"]["test"], ["CMD", *ops.HEALTH_COMMAND])

    def test_compose_owner_mismatch_refused(self):
        with self.assertRaisesRegex(ops.OpsError, "runtime UID"):
            ops.compose_document(self.data, OLD, 8080, os.getuid() + 1, os.getgid())

    def test_compose_public_data_directory_refused(self):
        self.data.chmod(0o755)
        with self.assertRaisesRegex(ops.OpsError, "private"):
            ops.compose_document(self.data, OLD, 8080, os.getuid(), os.getgid())

    def test_compose_shared_writable_database_refused(self):
        (self.data / "folio.db").chmod(0o666)
        with self.assertRaisesRegex(ops.OpsError, "private"):
            ops.compose_document(self.data, OLD, 8080, os.getuid(), os.getgid())

    def test_compose_sqlite_default_permissions_inside_private_root(self):
        (self.data / "folio.db").chmod(0o644)
        ops.compose_document(self.data, OLD, 8080, os.getuid(), os.getgid())

    def test_doctor_flags_permissions_without_exposing_token(self):
        self.data.chmod(0o755)
        with patch.object(ops, "run", return_value="synthetic tool version"):
            result = ops.doctor(self.data)
        self.assertFalse(next(c["ok"] for c in result["checks"] if c["check"] == "data_permissions"))
        self.assertNotIn(TEST_TOKEN, json.dumps(result))

    def test_unpinned_image_root_and_privileged_port_refused(self):
        for image, port, uid in [("folio:latest", 8080, 501), (OLD, 80, 501), (OLD, 8080, 0)]:
            with self.assertRaises(ops.OpsError):
                ops.compose_document(self.data, image, port, uid, 20)

    def test_compose_dollar_interpolation_refused(self):
        path = self.base / "$DATA"
        data_fixture(path)
        with self.assertRaises(ops.OpsError):
            ops.compose_document(path, OLD, 8080, 501, 20)

    def test_cli_failure_is_redacted(self):
        binary = self.base / "bad-folio"
        binary.write_text("#!/bin/sh\nprintf '%s' \"$FOLIO_TOKEN\" >&2\nexit 1\n")
        binary.chmod(0o700)
        with self.assertRaises(ops.OpsError) as caught:
            ops.folio_call(binary, "http://localhost", self.data / "token", ["call", "backup.export"])
        self.assertNotIn(TEST_TOKEN, str(caught.exception))


class ImageTests(Fixture):
    def elf(self, machine=183, dynamic=False):
        raw = bytearray(120)
        raw[:6] = b"\x7fELF\x02\x01"
        struct.pack_into("<H", raw, 18, machine)
        struct.pack_into("<Q", raw, 32, 64)
        struct.pack_into("<HH", raw, 54, 56, 1)
        struct.pack_into("<I", raw, 64, 3 if dynamic else 1)
        binary = self.base / "folio-linux"
        binary.write_bytes(raw)
        return binary

    def test_static_artifact_staging(self):
        binary = self.elf()
        ca = self.base / "ca.crt"
        ca.write_text("-----BEGIN CERTIFICATE-----\nsynthetic fixture\n-----END CERTIFICATE-----\n")
        out = self.base / "dist"
        ops.prepare_image(binary, ops.sha256(binary), ca, ops.sha256(ca), out, "arm64")
        self.assertEqual(ops.sha256(out / "folio"), ops.sha256(binary))
        self.assertEqual((out / "folio").stat().st_mode & 0o777, 0o555)
        self.assertTrue(json.loads((out / "provenance.json").read_text())["static"])

    def test_dynamic_elf_refused(self):
        with self.assertRaises(ops.OpsError):
            ops.static_elf(self.elf(dynamic=True), "arm64")

    def test_architecture_mismatch_refused(self):
        with self.assertRaises(ops.OpsError):
            ops.static_elf(self.elf(), "amd64")

    def test_non_elf_refused(self):
        binary = self.base / "folio"
        binary.write_bytes(b"not ELF")
        with self.assertRaises(ops.OpsError):
            ops.static_elf(binary, "arm64")

    def test_wrong_sha_refused(self):
        binary = self.elf()
        with self.assertRaises(ops.OpsError):
            ops.prepare_image(binary, "0" * 64, self.base / "missing", "0" * 64, self.base / "out", "arm64")


class ProbeTests(unittest.TestCase):
    @contextlib.contextmanager
    def server(self, status=200, body=None, redirect=False):
        raw = json.dumps(body or {"status": "ok", "version": "0.1.0"}).encode()
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(status)
                if redirect:
                    self.send_header("Location", "http://127.0.0.1:1")
                self.end_headers()
                self.wfile.write(raw)
            def log_message(self, *args):
                pass
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            yield f"http://127.0.0.1:{server.server_port}"
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_health_and_readiness(self):
        with self.server() as url:
            self.assertEqual(ops.probe(url), {"status": "ok", "version": "0.1.0"})
            self.assertEqual(ops.probe(url, ready=True)["status"], "ok")

    def test_bad_status_refused(self):
        with self.server(status=503) as url, self.assertRaises(ops.OpsError):
            ops.probe(url)

    def test_bad_body_refused(self):
        with self.server(body={"status": "failed"}) as url, self.assertRaises(ops.OpsError):
            ops.probe(url)

    def test_redirect_refused(self):
        with self.server(status=302, redirect=True) as url, self.assertRaises(ops.OpsError):
            ops.probe(url)


class FakeEngine:
    """Models Docker lifecycle only; not evidence of a real container run."""
    def __init__(self, root, fail_new=False, missing=False, backup_mutation=False):
        self.root, self.fail_new, self.missing = root, fail_new, missing
        self.running, self.image, self.events = True, OLD, []

    def present(self, image):
        self.events.append(("present", image))
        if self.missing:
            raise ops.OpsError("image absent")

    def stop(self, config):
        self.events.append(("stop", str(config)))
        self.running = False

    def start(self, config):
        image = json.loads(config.read_bytes())["services"]["blog"]["image"]
        self.events.append(("start", image))
        self.image, self.running = image, True
        if image == NEW:
            with sqlite3.connect(self.root / "folio.db") as conn:
                conn.execute("INSERT INTO test_posts VALUES (2, 'new schema/data', X'02')")

    def wait(self, url):
        self.events.append(("wait", self.image))
        if self.image == NEW and self.fail_new:
            raise ops.OpsError("synthetic health failure")


class DeploymentTests(Fixture):
    def setUp(self):
        super().setUp()
        self.config = self.base / "compose.json"
        # CI may run as root. Patch identity only for config generation in those tests.
        self.uid = os.getuid() or 1000
        ops.write_private(self.config, ops.json_bytes(ops.compose_document(self.data, OLD, 8080, self.uid, os.getgid())))

    @contextlib.contextmanager
    def identity(self):
        # The actual workstation is non-root; root CI exercises the same owner precondition.
        with patch.object(ops.os, "getuid", return_value=self.uid):
            if self.data.stat().st_uid != self.uid:
                self.skipTest("deployment ownership tests require a non-root runner")
            yield

    def count(self):
        with sqlite3.connect(self.data / "folio.db") as conn:
            return conn.execute("SELECT COUNT(*) FROM test_posts").fetchone()[0]

    def test_successful_upgrade_and_manual_rollback(self):
        engine = FakeEngine(self.data)
        with self.identity():
            result = ops.upgrade(self.config, NEW, self.base / "backups", engine)
            self.assertEqual(result["status"], "deployed")
            self.assertEqual(self.count(), 2)
            rolled = ops.rollback(self.config, engine)
        self.assertEqual(rolled["status"], "rolled_back")
        self.assertEqual(self.count(), 1)
        self.assertTrue(Path(rolled["preserved_data"]).is_dir())
        self.assertEqual(json.loads(self.config.read_bytes())["services"]["blog"]["image"], OLD)

    def test_health_failure_auto_restores_data_and_image(self):
        engine = FakeEngine(self.data, fail_new=True)
        with self.identity(), self.assertRaisesRegex(ops.OpsError, "previous image"):
            ops.upgrade(self.config, NEW, self.base / "backups", engine)
        self.assertEqual(self.count(), 1)
        self.assertEqual(engine.image, OLD)
        journal = json.loads(self.config.with_suffix(".upgrade.json").read_bytes())
        self.assertEqual(journal["status"], "rolled_back")
        self.assertTrue(Path(journal["failed_data"]).exists())

    def test_absent_new_image_does_not_stop_service(self):
        engine = FakeEngine(self.data, missing=True)
        with self.identity(), self.assertRaises(ops.OpsError):
            ops.upgrade(self.config, NEW, self.base / "backups", engine)
        self.assertTrue(engine.running)
        self.assertFalse(any(e[0] == "stop" for e in engine.events))

    def test_backup_failure_restarts_old_service(self):
        engine = FakeEngine(self.data)
        (self.data / "link").symlink_to(self.base)
        with self.identity(), self.assertRaises(ops.OpsError):
            ops.upgrade(self.config, NEW, self.base / "backups", engine)
        self.assertTrue(engine.running)
        self.assertEqual(engine.image, OLD)

    def test_tampered_backup_prevents_rollback(self):
        engine = FakeEngine(self.data)
        with self.identity():
            result = ops.upgrade(self.config, NEW, self.base / "backups", engine)
            Path(result["backup"]).write_bytes(b"tampered")
            with self.assertRaises(ops.OpsError):
                ops.rollback(self.config, engine)
        self.assertEqual(engine.image, NEW)

    def test_security_config_changes_refused(self):
        doc = json.loads(self.config.read_bytes())
        doc["services"]["blog"]["ports"] = ["0.0.0.0:8080:8080"]
        self.config.write_bytes(ops.json_bytes(doc))
        with self.assertRaises(ops.OpsError):
            ops.deployment(self.config)

    def test_concurrent_operation_refused(self):
        with ops.deployment_lock(self.config), self.assertRaises(ops.OpsError):
            with ops.deployment_lock(self.config):
                pass


class LogicalTests(Fixture):
    def setUp(self):
        super().setUp()
        self.binary = self.base / "folio-fixture"
        fixture = Path(__file__).parent / "fixtures" / "folio_fixture.py"
        self.binary.write_text("#!" + sys.executable + "\n" + fixture.read_text())
        self.binary.chmod(0o700)

    def test_logical_export_and_restore_contract(self):
        snapshot = self.base / "logical.json"
        result = ops.logical_export(self.binary, "http://127.0.0.1:8080", self.data / "token", snapshot)
        self.assertFalse(result["contains_token"])
        self.assertNotIn(TEST_TOKEN, snapshot.read_text())
        self.assertEqual(snapshot.stat().st_mode & 0o777, 0o600)
        self.assertEqual(ops.logical_restore(self.binary, "http://127.0.0.1:8080", self.data / "token", snapshot)["status"], "restored")

    def test_unknown_format_refused(self):
        bad = self.base / "bad.json"
        bad.write_text('{"format":"unknown"}')
        with self.assertRaises(ops.OpsError):
            ops.logical_restore(self.binary, "http://localhost", self.data / "token", bad)

    def test_logical_size_limit(self):
        with patch.object(ops, "MAX_LOGICAL", 16), self.assertRaises(ops.OpsError):
            ops.logical_export(self.binary, "http://localhost", self.data / "token", self.base / "large.json")

    def test_isolated_logical_drill(self):
        result = ops.logical_drill(self.binary, "http://127.0.0.1:8080", self.data / "token", self.base / "drill")
        self.assertEqual(result["status"], "drill_passed")
        self.assertTrue(result["independent_token"])
        self.assertTrue(result["restore_audit_verified"])
        self.assertNotEqual(result["source_state_sha256"], result["restored_state_sha256"])

    def test_supported_backup_versions_and_matching_schemas(self):
        for version in (1, 2):
            ops.validate_snapshot({"format": "folio-backup", "version": version,
                                   "state": {"schema": version}, "sha256": "a" * 64})

    def test_invalid_backup_version_or_schema_refused(self):
        for version, schema in ((1, 2), (2, 1), (3, 3), (True, 1), (2, True), (2, "2"), (2, None)):
            with self.subTest(version=version, schema=schema), self.assertRaises(ops.OpsError):
                ops.validate_snapshot({"format": "folio-backup", "version": version,
                                       "state": {"schema": schema}, "sha256": "a" * 64})

    def test_restore_reports_schema_upgrade(self):
        snapshot = self.base / "v1.json"
        snapshot.write_text(json.dumps({"format": "folio-backup", "version": 1,
                                       "state": {"schema": 1}, "sha256": "a" * 64}))
        with patch.object(ops, "folio_call", return_value={"ok": True, "data": {
                "restored": True, "source_schema": 1, "schema": 2, "schema_upgraded": True}}):
            result = ops.logical_restore(self.binary, "http://localhost", self.data / "token", snapshot)
        self.assertTrue(result["schema_upgraded"])
        self.assertEqual((result["source_schema"], result["schema"]), (1, 2))


class RestoreStateTests(unittest.TestCase):
    def snapshots(self, audit_count=1):
        state = {"schema": 1, "revision": 20, "settings": {"title": "test"},
                 "settings_revision": 2, "posts": {"a": {"markdown": "keep"}},
                 "media": {"a.png": {"data": "AQ=="}}, "idempotency": {},
                 "audit": [{"id": str(i)} for i in range(audit_count)]}
        after = copy.deepcopy(state)
        after["revision"] += 1
        after["audit"] = (after["audit"] + [{"id": "a" * 24, "at": "2026-10-03T00:00:00Z",
                 "operation": "backup.restore", "target": "instance", "actor": "admin", "revision": 20}])[-10000:]
        return {"state": state}, {"state": after}

    def test_expected_audit_and_revision_change(self):
        ops.verify_restored_state(*self.snapshots())

    def test_v2_workflow_maps_preserved_exactly(self):
        source, target = self.snapshots()
        for snapshot in (source, target):
            snapshot["version"] = snapshot["state"]["schema"] = 2
            snapshot["state"]["proposals"] = {"a": {"status": "pending", "candidate": "private"}}
            snapshot["state"]["schedules"] = {"b": {"status": "pending", "snapshot": "pinned"}}
        self.assertFalse(ops.verify_restored_state(source, target)["schema_upgraded"])
        for field in ("proposals", "schedules"):
            altered = copy.deepcopy(target)
            altered["state"][field] = {}
            with self.assertRaises(ops.OpsError):
                ops.verify_restored_state(source, altered)

    def test_v1_to_v2_only_declared_schema_migration(self):
        source, target = self.snapshots()
        source["version"], target["version"] = 1, 2
        target["state"]["schema"] = 2
        for empty_maps in ({}, {"proposals": {}, "schedules": {}}):
            candidate = copy.deepcopy(target)
            candidate["state"].update(empty_maps)
            result = ops.verify_restored_state(source, candidate)
            self.assertTrue(result["schema_upgraded"])
            self.assertEqual((result["source_schema"], result["schema"]), (1, 2))

    def test_migration_cannot_discard_or_create_workflow_data(self):
        for changed_side in (0, 1):
            for field in ("proposals", "schedules"):
                source, target = self.snapshots()
                source["version"], target["version"] = 1, 2
                target["state"]["schema"] = 2
                (source, target)[changed_side]["state"][field] = {"unexpected": {"id": "x"}}
                with self.assertRaises(ops.OpsError):
                    ops.verify_restored_state(source, target)

    def test_schema_downgrade_or_unknown_migration_refused(self):
        for before, after in ((2, 1), (1, 3)):
            source, target = self.snapshots()
            source["state"]["schema"], target["state"]["schema"] = before, after
            with self.assertRaises(ops.OpsError):
                ops.verify_restored_state(source, target)

    def test_fractional_utc_restore_timestamp(self):
        for fraction in ("1", "123", "123456789"):
            source, target = self.snapshots()
            target["state"]["audit"][-1]["at"] = "2026-10-03T00:00:00." + fraction + "Z"
            ops.verify_restored_state(source, target)

    def test_malformed_restore_timestamp_refused(self):
        for timestamp in ("2026-10-03T00:00:00.Z", "2026-10-03T00:00:00.1234567890Z",
                          "2026-10-03T00:00:00.12xZ", "2026-10-03T00:00:00+00:00",
                          "2026-02-30T00:00:00.123Z", "2026-10-03T25:00:00.123Z"):
            source, target = self.snapshots()
            target["state"]["audit"][-1]["at"] = timestamp
            with self.subTest(timestamp=timestamp), self.assertRaises(ops.OpsError):
                ops.verify_restored_state(source, target)

    def test_audit_retention_boundary(self):
        ops.verify_restored_state(*self.snapshots(10000))

    def test_content_change_refused(self):
        source, target = self.snapshots()
        target["state"]["posts"]["a"]["markdown"] = "changed"
        with self.assertRaises(ops.OpsError):
            ops.verify_restored_state(source, target)

    def test_missing_extra_or_wrong_audit_refused(self):
        for mutation in (lambda s: s["audit"].pop(),
                         lambda s: s["audit"].append(s["audit"][-1]),
                         lambda s: s["audit"][-1].update(actor="read"),
                         lambda s: s["audit"][0].update(id="changed")):
            source, target = self.snapshots()
            mutation(target["state"])
            with self.assertRaises(ops.OpsError):
                ops.verify_restored_state(source, target)

    def test_wrong_revision_refused(self):
        source, target = self.snapshots()
        target["state"]["revision"] += 1
        with self.assertRaises(ops.OpsError):
            ops.verify_restored_state(source, target)


class SchedulerSupportTests(unittest.TestCase):
    def test_versions_keep_v1_compatibility_and_pause_v2(self):
        for version, expected in (("0.1.0", False), ("0.2.0", True), ("0.2.1", True), ("1.0.0", True)):
            run = subprocess.CompletedProcess([], 0, json.dumps({"version": version}).encode(), b"")
            with patch.object(ops.subprocess, "run", return_value=run):
                self.assertEqual(ops.supports_paused_schedules(Path("/trusted/folio")), expected)

    def test_unknown_version_fails_closed(self):
        run = subprocess.CompletedProcess([], 0, b'{"version":"unknown"}', b"")
        with patch.object(ops.subprocess, "run", return_value=run), self.assertRaises(ops.OpsError):
            ops.supports_paused_schedules(Path("/trusted/folio"))


if __name__ == "__main__":
    unittest.main()
