#!/usr/bin/env python3
"""Exercise ops against a real Linux Folio binary; only disposable local data.

Usage: python3 ops/tests/integration.py --binary dist/folio
No Docker daemon, external deployment, downloads or source data is used.
"""
from __future__ import annotations

import argparse
import base64
import contextlib
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile
import time
import urllib.request

OPS_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(OPS_ROOT))
import ops

PNG = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")


class Integration:
    def __init__(self, binary, root):
        self.binary, self.root = binary, root
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("FOLIO_")}
        self.processes, self.tokens, self.outputs, self.checks = [], [], [], []

    def command(self, argv, env=None, success=True):
        run = subprocess.run([str(a) for a in argv], env=env or self.env,
                             capture_output=True, timeout=60)
        self.outputs.extend([run.stdout, run.stderr])
        if (run.returncode == 0) != success:
            raise AssertionError("Unexpected command exit: " + str(argv[1]))
        data = run.stdout if run.returncode == 0 else (run.stdout or run.stderr)
        return json.loads(data)

    def operation(self, server, name, payload):
        env = self.env | {"FOLIO_URL": server["url"], "FOLIO_TOKEN": server["token"]}
        return self.command([server["binary"], "call", name, "--json", json.dumps(payload)], env)["data"]

    def tool(self, name, *args, success=True):
        return self.command([sys.executable, OPS_ROOT / "ops.py", name, *args], success=success)

    def check(self, description):
        self.checks.append(description)
        print("PASS " + description, flush=True)

    def start(self, name, initialize=True, binary=None, pause_schedules=True):
        binary = self.binary if binary is None else binary
        data = self.root / name
        if initialize:
            assert self.command([binary, "init", "--data", data])["initialized"]
        token = ops.token_file(data / "token")
        self.tokens.append(token)
        logpath = self.root / (name + ".log")
        paused = pause_schedules and ops.supports_paused_schedules(binary, self.env)
        command = [str(binary), "serve", "--data", str(data), "--addr", "127.0.0.1:0"]
        if paused:
            command.append("--pause-schedules")
        with logpath.open("wb") as log:
            proc = subprocess.Popen(command,
                                    env=self.env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        self.processes.append((proc, logpath))
        deadline = time.monotonic() + 10
        while True:
            assert proc.poll() is None, "Server exited during initialization"
            match = re.search(r"http://127\.0\.0\.1:\d+", logpath.read_text())
            if match:
                url = match.group()
                break
            assert time.monotonic() < deadline, "Server startup timed out"
            time.sleep(.03)
        assert self.tool("probe", "--url", url)["status"] == "ok"
        server = {"data": data, "token": token, "proc": proc, "url": url, "binary": binary}
        if paused:
            assert self.operation(server, "system.info", {})["scheduler"]["paused"] is True
        return server

    def export(self, server, name):
        path = self.root / name
        result = self.tool("logical-export", "--binary", self.binary, "--url", server["url"],
                           "--token-file", server["data"] / "token", "--output", path)
        assert result["contains_token"] is False
        assert path.stat().st_mode & 0o777 == 0o600
        self.outputs.append(path.read_bytes())
        return path, json.loads(path.read_bytes())

    def restore(self, server, path, success=True):
        return self.tool("logical-restore", "--binary", self.binary, "--url", server["url"],
                         "--token-file", server["data"] / "token", "--backup", path, success=success)

    def fetch(self, server, path):
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), ops.NoRedirect())
        with opener.open(server["url"] + path, timeout=3) as response:
            assert response.status == 200
            data = response.read()
            self.outputs.append(data)
            return data

    def stop(self, proc):
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
                raise AssertionError("Server did not stop gracefully") from None
        assert proc.returncode == 0, "Server returned nonzero on shutdown"

    def finish(self):
        for proc, log in reversed(self.processes):
            self.stop(proc)
            self.outputs.append(log.read_bytes())
        for secret in self.tokens:
            assert not any(secret.encode() in output for output in self.outputs), "Credential leaked into command output, logical backup or daemon logs"


def exercise(h):
    architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    assert platform.system() == "Linux" and architecture, "This integration suite needs a Linux amd64/arm64 executable"
    ops.static_elf(h.binary, architecture)
    version = h.command([h.binary, "version"])["version"]
    assert version.startswith("0.2."), "The workflow suite requires a v0.2 Folio binary"
    for language in ("zh-CN", "en"):
        assert h.command([h.binary, "--lang", language, "version"])["version"] == version
        assert h.command([h.binary, "version"], h.env | {"FOLIO_LANG": language})["version"] == version
    h.check("real static Linux ELF and version " + version)
    source = h.start("source")
    doctor = h.tool("doctor", "--data", source["data"])
    assert next(c["ok"] for c in doctor["checks"] if c["check"] == "data_permissions")
    assert h.tool("probe", "--url", source["url"], "--ready")["status"] == "ok"
    h.command([h.binary, "healthcheck", "--url", source["url"] + "/healthz"])
    h.check("real init, private data, doctor, healthcheck and readiness probes")

    post = h.operation(source, "posts.create", {"title": "Ops recovery test", "slug": "ops-recovery", "markdown": "# Public checkpoint\n\nPublished content."})["post"]
    h.operation(source, "posts.publish", {"id": post["id"], "expected_revision": post["revision"], "confirm": True})
    draft = h.operation(source, "posts.update", {"id": post["id"], "expected_revision": post["revision"],
                "title": "Private next version", "slug": "ops-recovery", "markdown": "# Private draft\n\nUnpublished changes."})["post"]
    media = h.operation(source, "media.upload", {"name": "ops.png", "base64": base64.b64encode(PNG).decode()})["media"]
    proposal = h.operation(source, "proposals.create", {"candidate": {"title": "Private proposal", "slug": "private-proposal",
                          "markdown": "Unreviewed candidate must survive recovery."}})["proposal"]
    due = dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=1)
    future = due.isoformat().replace("+00:00", "Z")
    schedule = h.operation(source, "schedules.create", {"post_id": post["id"], "expected_revision": draft["revision"],
                          "publish_at": future, "confirm": True})["schedule"]
    while dt.datetime.now(dt.timezone.utc) < due + dt.timedelta(seconds=1.1):
        time.sleep(.05)
    assert h.operation(source, "schedules.get", {"id": schedule["id"]})["schedule"]["status"] == "pending"
    h.check("paused recovery daemon keeps overdue pinned schedule pending across scheduler ticks")
    source_public = h.fetch(source, "/api/public/posts/ops-recovery")
    assert b"Private next version" not in source_public
    assert h.fetch(source, media["url"]) == PNG
    snapshot_path, snapshot = h.export(source, "logical.json")
    assert snapshot["version"] == snapshot["state"]["schema"] == 2
    assert snapshot["state"]["proposals"][proposal["id"]] == proposal
    assert snapshot["state"]["schedules"][schedule["id"]] == schedule
    h.check("v2 export preserves published/draft revisions, media, proposals and pinned schedules; excludes token")

    target = h.start("target")
    assert target["token"] != source["token"]
    restore_result = h.restore(target, snapshot_path)
    assert restore_result["token_preserved"] and restore_result["schema"] == 2 and not restore_result["schema_upgraded"]
    _, restored = h.export(target, "restored.json")
    ops.verify_restored_state(snapshot, restored)
    assert ops.token_file(target["data"] / "token") == target["token"]
    assert h.fetch(target, "/api/public/posts/ops-recovery") == source_public
    assert h.fetch(target, media["url"]) == PNG
    assert h.operation(target, "proposals.get", {"id": proposal["id"]})["proposal"] == proposal
    assert h.operation(target, "schedules.get", {"id": schedule["id"]})["schedule"] == schedule
    h.check("logical restore preserves content and independent token; exact restore audit/revision transition")

    h.restore(target, snapshot_path, success=False)
    _, after_rejection = h.export(target, "after-rejection.json")
    assert after_rejection["sha256"] == restored["sha256"]
    corrupt = h.root / "corrupt.json"
    corrupted = json.loads(snapshot_path.read_bytes())
    corrupted["sha256"] = "0" * 64
    ops.write_private(corrupt, ops.json_bytes(corrupted))
    empty = h.start("checksum-target")
    _, before_bad = h.export(empty, "before-bad.json")
    h.restore(empty, corrupt, success=False)
    _, after_bad = h.export(empty, "after-bad.json")
    assert before_bad["sha256"] == after_bad["sha256"]
    h.check("non-empty target and corrupt checksum refused without changing target state")

    drill = h.tool("logical-drill", "--binary", h.binary, "--url", source["url"],
                   "--token-file", source["data"] / "token", "--workdir", h.root / "drill")
    assert drill["status"] == "drill_passed" and drill["content_preserved"] and drill["restore_audit_verified"] and drill["scheduler_paused"]
    assert drill["source_state_sha256"] != drill["restored_state_sha256"]
    assert ops.token_file(h.root / "drill/data/token") != source["token"]
    _, source_after = h.export(source, "source-after-drill.json")
    assert snapshot["sha256"] == source_after["sha256"]
    h.check("isolated real-binary recovery drill passes; source checksum unchanged")

    config = h.root / "compose.json"
    h.tool("render", "--data", source["data"], "--image", "sha256:" + "a" * 64, "--output", config)
    document = json.loads(config.read_bytes())
    assert document["services"]["blog"]["user"] == f"{os.getuid()}:{os.getgid()}"
    assert h.tool("render", "--data", source["data"], "--image", "sha256:" + "a" * 64,
                  "--uid", os.getuid() + 1, "--output", h.root / "wrong-owner.json", success=False)["ok"] is False
    ops.deployment(config)
    h.check("Compose rendering keeps private numeric ownership and rejects a mismatched runtime UID")

    h.stop(source["proc"])
    offline = h.root / "offline.tar.gz"
    assert h.tool("offline-backup", "--stopped", "--data", source["data"], "--output", offline)["contains_token"]
    assert offline.stat().st_mode & 0o777 == 0o600
    assert h.tool("verify-offline", "--backup", offline)["sqlite"] == "ok"
    physical = h.root / "physical"
    assert h.tool("offline-restore", "--backup", offline, "--target", physical)["sqlite"] == "ok"
    assert physical.stat().st_mode & 0o777 == 0o700
    assert ops.token_file(physical / "token") == source["token"]
    physical_server = h.start("physical", initialize=False)
    _, physical_snapshot = h.export(physical_server, "physical.json")
    assert physical_snapshot["sha256"] == snapshot["sha256"]
    assert h.fetch(physical_server, "/api/public/posts/ops-recovery") == source_public
    assert h.fetch(physical_server, media["url"]) == PNG
    h.check("stopped backup, manifest/SQLite verification, physical restore and real restart preserve state and media")

    ca = Path("/etc/ssl/certs/ca-certificates.crt")
    if ca.is_file():
        staged = h.root / "image-dist"
        result = h.tool("prepare-image", "--binary", h.binary, "--sha256", ops.sha256(h.binary),
                        "--ca-bundle", ca, "--ca-sha256", ops.sha256(ca), "--arch", architecture, "--output", staged)
        assert result["binary_sha256"] == ops.sha256(h.binary)
        assert (staged / "folio").stat().st_mode & 0o777 == 0o555
        h.check("actual ELF and OS CA bundle hash verification/staging; no container run claimed")
    return doctor


def legacy_migration(h, binary):
    assert h.command([binary, "version"])["version"].startswith("0.1.")
    legacy = h.start("legacy-source", binary=binary)
    post = h.operation(legacy, "posts.create", {"title": "Legacy content", "slug": "legacy-content",
                                              "markdown": "# Legacy content\n\nPreserve exactly."})["post"]
    h.operation(legacy, "posts.publish", {"id": post["id"], "expected_revision": post["revision"], "confirm": True})
    media = h.operation(legacy, "media.upload", {"name": "legacy.png", "base64": base64.b64encode(PNG).decode()})["media"]
    legacy_path, source = h.export(legacy, "legacy-v1.json")
    assert source["version"] == source["state"]["schema"] == 1
    target = h.start("migration-target")
    result = h.restore(target, legacy_path)
    assert result["source_schema"] == 1 and result["schema"] == 2 and result["schema_upgraded"]
    _, restored = h.export(target, "migrated-v2.json")
    assert ops.verify_restored_state(source, restored)["schema_upgraded"]
    assert h.fetch(target, "/api/public/posts/legacy-content") == h.fetch(legacy, "/api/public/posts/legacy-content")
    assert h.fetch(target, media["url"]) == PNG
    drill = h.tool("logical-drill", "--binary", h.binary, "--url", legacy["url"],
                   "--token-file", legacy["data"] / "token", "--workdir", h.root / "legacy-drill")
    assert drill["schema_upgraded"] and drill["status"] == "drill_passed" and drill["scheduler_paused"]
    _, unchanged = h.export(legacy, "legacy-unchanged.json")
    assert unchanged["sha256"] == source["sha256"]
    h.check("real v0.1→v0.2 restore and drill explicitly report schema upgrade, preserve content/media and leave source unchanged")
    h.stop(target["proc"])
    database = target["data"] / "folio.db"
    before = ops.sha256(database)
    old = subprocess.run([str(binary), "serve", "--data", str(target["data"]), "--addr", "127.0.0.1:0"],
                         env=h.env, capture_output=True, timeout=5)
    h.outputs.extend([old.stdout, old.stderr])
    assert old.returncode != 0, "Old binary must refuse upgraded schema2"
    assert ops.sha256(database) == before, "Old binary changed upgraded database bytes"
    reopened = h.start("migration-target", initialize=False)
    _, protected = h.export(reopened, "protected-v2.json")
    assert protected["sha256"] == restored["sha256"]
    h.check("old v0.1 daemon rejects upgraded schema2 without rewriting database; v0.2 reopens unchanged")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--legacy-binary", type=Path, help="optional trusted v0.1 Linux executable for real migration coverage")
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="folio-real-ops-") as work:
        harness = Integration(binary, Path(work))
        try:
            doctor = exercise(harness)
            if args.legacy_binary:
                legacy_migration(harness, args.legacy_binary.resolve(strict=True))
        finally:
            harness.finish()
        harness.check("all owned daemons stopped gracefully; no credential in logs or logical exports")
        report = {"at_utc": dt.datetime.now(dt.timezone.utc).isoformat(), "status": "passed",
                  "binary_sha256": ops.sha256(binary), "python": platform.python_version(),
                  "platform": platform.system() + " " + platform.machine(), "checks": harness.checks,
                  "doctor": doctor, "container_run": "not performed", "runtime_data_retained": False}
        report["legacy_migration"] = "passed" if args.legacy_binary else "not requested"
        if args.legacy_binary:
            report["legacy_binary_sha256"] = ops.sha256(args.legacy_binary.resolve(strict=True))
        if args.report:
            args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({"status": "passed", "checks": len(harness.checks), "container_run": "not performed"}))


if __name__ == "__main__":
    main()
