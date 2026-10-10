#!/usr/bin/env python3
"""Real SIGKILL recovery gate: disposable databases, owned processes, stdlib only.

This tests process crashes with the kernel/filesystem still running, not power
loss or broken disks. Negative controls alter only separate, stopped lab copies.
"""
from __future__ import annotations

import argparse
from contextlib import closing
import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import sys
import tempfile
import time
import urllib.error
import urllib.request

sys.dont_write_bytecode = True

from e2e import encode, require
from workflows_e2e import WorkflowHarness, content, timestamp


class RecoveryMismatch(AssertionError):
    """Only a specific recovery invariant may satisfy a negative control."""


def require_recovery(condition, message):
    if not condition:
        raise RecoveryMismatch(message)


def kill_owned(h, server, name, report):
    proc = server["proc"]
    require(proc in h.processes and proc.poll() is None, "Cannot kill an unowned/dead daemon")
    database = h.root / name / "folio.db"
    before = database.stat()
    wal_bytes = database.with_name("folio.db-wal").stat().st_size
    require(wal_bytes > 32, "Fixture must leave committed frames in a nonempty WAL")
    os.kill(proc.pid, signal.SIGKILL)
    code = proc.wait(timeout=5)
    require(code == -signal.SIGKILL, "Daemon did not actually terminate by SIGKILL")
    after = database.stat()
    require((before.st_dev, before.st_ino) == (after.st_dev, after.st_ino), "Database was replaced")
    report["crashes"].append({"case": name, "signal": "SIGKILL", "returncode": code,
                              "wal_bytes_before_kill": wal_bytes, "database_file_unchanged": True})
    print("PASS actual SIGKILL: " + name + " returncode=" + str(code), flush=True)


def restart_same(h, name, before, report, pause=False):
    database = h.root / name / "folio.db"
    original = database.stat()
    server = h.start(name, initialize=False, pause=pause)
    info = h.call(server, "system.info")["data"]
    current = database.stat()
    require(info["instance_id"] == before["instance_id"], "Restart changed instance identity")
    require((original.st_dev, original.st_ino) == (current.st_dev, current.st_ino), "Restart replaced database")
    report["restarts"].append({"case": name, "initialize": False, "same_database_file": True,
                               "same_instance_id": True, "scheduler_paused": pause})
    return server


def clone_stopped(h, name, clone_name, mutate):
    """Copy the entire stopped lab instance including WAL before modifying it."""
    source, target = h.root / name, h.root / clone_name
    require(source.parent == h.root and target.parent == h.root and not target.exists(), "Invalid lab clone")
    shutil.copytree(source, target)
    with closing(sqlite3.connect(target / "folio.db", timeout=5)) as conn:
        with conn:
            require(conn.execute("PRAGMA integrity_check").fetchone() == ("ok",), "Lab clone is corrupt")
            document = json.loads(conn.execute("SELECT document FROM state WHERE id=1").fetchone()[0])
            mutate(document)
            conn.execute("UPDATE state SET document=? WHERE id=1", (encode(document).decode(),))
    return clone_name


def replay_receipt(h, server, operation, args, expected):
    # Unlike Harness.operation, capture an error response too: the missing-key
    # control must fail on its changed receipt, not merely on harness startup.
    request = urllib.request.Request(server["url"] + "/api/op/" + operation,
        data=encode(args), method="POST", headers={"Content-Type": "application/json",
        "Authorization": "Bearer " + server["token"]})
    try:
        response = urllib.request.urlopen(request, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        h.transcripts.append(raw)
        value = json.loads(raw)
        require_recovery(response.status == 200 and value.get("ok") is True
                         and value.get("data") == expected,
                         "Exact " + operation + " receipt was lost/changed (HTTP " + str(response.status) + ")")


def inspect_private_head(h, server, first, second, before):
    actual = h.call(server, "posts.get", {"id": first["id"]})["data"]
    require(actual["post"] == second, "Acknowledged private head changed across crash")
    require(sorted(actual["revisions"], key=lambda p: p["revision"]) == [first, second], "History missing/duplicated")
    require(h.call(server, "posts.list")["data"]["posts"] == [second], "Post missing/duplicated")
    require(h.call(server, "system.info")["data"]["revision"] == before["revision"], "Unexpected write on restart/retry")
    h.assert_private(server, second, "CRASH_ACK_PRIVATE")


def acknowledged_retry(h, report):
    name = "acknowledged-write"
    server = h.start(name)
    create_args = {"title": "CRASH_ACK_PRIVATE 双语草稿", "slug": "crash-ack-private",
        "markdown": "# 原文\n\nCRASH_ACK_PRIVATE original saved text.\n", "excerpt": "私有 / Private",
        "tags": ["中文", "English"], "category": "Process crash / 进程崩溃", "cover": "", "featured": False,
        "idempotency_key": "crash-create-once"}
    created = h.operation(server, "posts.create", create_args)["data"]
    first = created["post"]
    update_args = content(first) | {"id": first["id"], "expected_revision": 1, "featured": True,
        "markdown": "# 后续原文\n\nCRASH_ACK_PRIVATE acknowledged R2; not published.\n",
        "idempotency_key": "crash-update-once"}
    updated = h.operation(server, "posts.update", update_args)["data"]
    second = updated["post"]
    require(first["revision"] == 1 and second["revision"] == 2, "Invalid acknowledged fixture")
    before = h.call(server, "system.info")["data"]
    audit = h.call(server, "audit.list")["data"]
    kill_owned(h, server, name, report)

    def drop_receipt(document):
        matches = [key for key in document["idempotency"] if key.endswith(":crash-create-once")]
        require(len(matches) == 1, "Negative control must remove exactly one persisted create receipt")
        del document["idempotency"][matches[0]]

    control = clone_stopped(h, name, "control-missing-receipt", drop_receipt)
    server = restart_same(h, name, before, report)
    inspect_private_head(h, server, first, second, before)
    replay_receipt(h, server, "posts.create", create_args, created)
    replay_receipt(h, server, "posts.update", update_args, updated)
    inspect_private_head(h, server, first, second, before)
    require(h.call(server, "audit.list")["data"] == audit, "Retry duplicated audit/write")
    report["acknowledged_retry"] = {"create_receipt_identical": True, "update_receipt_identical": True,
        "post_count": 1, "history_revisions": [1, 2], "current_revision": 2, "audit_unchanged": True,
        "instance_revision_unchanged": True, "private_on_all_checked_surfaces": True}
    h.check("acknowledged R1/R2 survive SIGKILL; identical keys return original receipts without writes")
    h.stop(server["proc"])

    faulty = h.start(control, initialize=False)
    inspect_private_head(h, faulty, first, second, before)
    try:
        replay_receipt(h, faulty, "posts.create", create_args, created)
    except RecoveryMismatch as error:
        require(str(error) == "Exact posts.create receipt was lost/changed (HTTP 409)", "Unexpected control failure")
        report["negative_controls"].append({"fault": "delete create receipt in stopped lab clone",
            "startup_healthy": True, "unchanged_R2_verified": True, "rejected_by": str(error)})
    else:
        raise AssertionError("Missing-receipt negative control escaped the actual recovery checker")
    h.check("negative control rejects lost persisted retry receipt on a healthy isolated clone")
    h.stop(faulty["proc"])


def inspect_pinned(h, server, first, second, scheduled, terminal=None):
    job = h.call(server, "schedules.get", {"id": scheduled["id"]})["data"]["schedule"]
    current = h.call(server, "posts.get", {"id": first["id"]})["data"]
    live = current["live"]
    require_recovery(job["post_revision"] == 1 and content(job["snapshot"]) == content(first)
                     and live is not None and live["revision"] == 1 and content(live) == content(first),
                     "Pinned R1 drifted to unapproved R2")
    require(job["status"] == "published" and job["revision"] == 2 and job["executed_at"], "Nonterminal job")
    require(job["publish_at"] == scheduled["publish_at"], "Approved publication time changed")
    if terminal is not None:
        require(job == terminal, "Terminal receipt changed on second crash recovery/tick")
    require(content(current["post"]) == content(second) and current["post"]["revision"] == 2
            and current["post"]["updated_at"] == second["updated_at"]
            and current["post"]["status"] == "changed" and current["post"]["published_revision"] == 1,
            "Scheduled activation lost or changed private R2")
    require(sorted(current["revisions"], key=lambda p: p["revision"]) == [first, second], "Scheduled activation changed history")
    h.audit_once(server, first["id"], scheduled["id"])
    return job


def pinned_publication(h, report):
    name = "pinned-schedule"
    # Pausing execution keeps the fixture pending even on a slow runner. It
    # changes no stored approval; restart without the flag executes the real job.
    server = h.start(name, pause=True)
    first = h.operation(server, "posts.create", {"title": "CRASH_APPROVED_R1 已审阅", "slug": "crash-approved-r1",
        "markdown": "# 已审阅\n\nCRASH_APPROVED_R1 only this text is approved.\n", "excerpt": "已批准 / Approved",
        "tags": ["已审阅", "approved"], "category": "Reviewed", "cover": "", "featured": True})["data"]["post"]
    due = time.time() + 2
    schedule_args = {"post_id": first["id"], "expected_revision": 1, "publish_at": timestamp(due),
                     "confirm": True, "idempotency_key": "crash-approved-schedule-once"}
    scheduled_result = h.operation(server, "schedules.create", schedule_args)["data"]
    scheduled = scheduled_result["schedule"]
    require(scheduled["post_revision"] == 1 and content(scheduled["snapshot"]) == content(first), "R1 not pinned")
    second = h.operation(server, "posts.update", content(first) | {"id": first["id"], "expected_revision": 1,
        "title": "CRASH_PRIVATE_R2 未批准", "slug": "crash-private-r2", "excerpt": "CRASH_PRIVATE_R2 私有",
        "markdown": "CRASH_PRIVATE_R2 must stay private.\n", "tags": ["CRASH_PRIVATE_R2"],
        "category": "CRASH_PRIVATE_R2", "featured": False})["data"]["post"]
    require(second["revision"] == 2, "Invalid later-draft fixture")
    require(h.call(server, "schedules.get", {"id": scheduled["id"]})["data"] == scheduled_result, "Job not pending")
    h.assert_private(server, first, "CRASH_APPROVED_R1")
    h.assert_private(server, second, "CRASH_PRIVATE_R2")
    before = h.call(server, "system.info")["data"]
    kill_owned(h, server, name, report)

    def drift_snapshot(document):
        job = document["schedules"][scheduled["id"]]
        require(job["status"] == "pending" and job["post_revision"] == 1, "Invalid control source")
        job["snapshot"] = copy.deepcopy(second)
        job["post_revision"] = 2

    control = clone_stopped(h, name, "control-drifted-snapshot", drift_snapshot)
    # Bounded offline wait, independent of how long the fixture took to prepare.
    deadline = time.monotonic() + 3
    while time.time() <= due:
        require(time.monotonic() < deadline, "Clock prevented bounded overdue fixture")
        time.sleep(0.05)
    server = restart_same(h, name, before, report)
    h.await_schedule(server, scheduled["id"], "published", timeout=5)
    terminal = inspect_pinned(h, server, first, second, scheduled)
    replay_receipt(h, server, "schedules.create", schedule_args, scheduled_result)
    require(h.call(server, "schedules.list")["data"]["schedules"] == [terminal], "Schedule retry duplicated a job")
    public = json.loads(h.http(server, "/api/public/posts/" + first["slug"]))["post"]
    require(content(public) == content(first) and public["revision"] == 1, "Public API differs from approved R1")
    for path in ("/api/public/site", "/rss.xml", "/sitemap.xml", "/", "/posts/" + first["slug"]):
        body = h.http(server, path).decode()
        require("CRASH_PRIVATE_R2" not in body and second["slug"] not in body, "Private R2 content/URL leaked")
    require(not json.loads(h.http(server, "/api/public/search?q=CRASH_PRIVATE_R2"))["posts"], "Private R2 leaked in search")
    require(len(json.loads(h.http(server, "/api/public/search?q=CRASH_APPROVED_R1"))["posts"]) == 1, "Approved R1 missing from search")
    h.http(server, "/api/public/posts/" + second["slug"], 404)
    stable = h.call(server, "system.info")["data"]
    audit = h.call(server, "audit.list")["data"]
    kill_owned(h, server, name, report)
    server = restart_same(h, name, stable, report)
    inspect_pinned(h, server, first, second, scheduled, terminal)
    # Cross a full real one-second worker interval without a long blocking sleep.
    deadline = time.monotonic() + 1.2
    while time.monotonic() < deadline:
        inspect_pinned(h, server, first, second, scheduled, terminal)
        time.sleep(0.1)
    require(h.call(server, "audit.list")["data"] == audit and
            h.call(server, "system.info")["data"]["revision"] == stable["revision"], "Terminal restart/tick wrote again")
    report["pinned_schedule"] = {"approved_revision": 1, "private_head_revision": 2, "published_revision": 1,
        "schedule_revision": 2, "schedule_receipt_identical_on_retry": True, "terminal_unchanged_after_second_SIGKILL": True,
        "scheduler_posts_publish_events": 1, "scheduler_schedules_publish_events": 1,
        "audit_and_instance_revision_unchanged_after_second_restart": True, "private_R2_absent_from_public_surfaces": True}
    h.check("approved pinned R1 publishes once after SIGKILL; private R2 survives; terminal survives second SIGKILL/ticks")
    h.stop(server["proc"])

    faulty = h.start(control, initialize=False)
    h.await_schedule(faulty, scheduled["id"], "published", timeout=5)
    try:
        inspect_pinned(h, faulty, first, second, scheduled)
    except RecoveryMismatch as error:
        require(str(error) == "Pinned R1 drifted to unapproved R2", "Unexpected control failure")
        report["negative_controls"].append({"fault": "replace pinned snapshot with R2 in stopped lab clone",
            "startup_healthy": True, "job_executed": True, "rejected_by": str(error)})
    else:
        raise AssertionError("Drifted-snapshot negative control escaped the actual recovery checker")
    h.check("negative control rejects unapproved publication after isolated pinned-snapshot drift")
    h.stop(faulty["proc"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    require(hasattr(signal, "SIGKILL") and os.name == "posix", "Real SIGKILL gate requires POSIX; cannot silently skip")
    report = {"scope": "real process crash, kernel/filesystem alive; not power-loss/disk-fault proof",
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "crashes": [], "restarts": [], "negative_controls": [], "passed": False}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="folio-sigkill-") as directory:
        h = WorkflowHarness(binary, Path(directory))
        try:
            acknowledged_retry(h, report)
            pinned_publication(h, report)
            h.close()
            h.check("owned processes cleaned; no bearer tokens in CLI/public output or daemon logs")
            report["passed"] = True
        except Exception as error:
            message = str(error)
            for token in h.secrets:
                message = message.replace(token, "[REDACTED]")
            report["failure"] = {"type": type(error).__name__, "message": message}
            raise
        finally:
            # Preserve sanitized diagnostics before the owned temporary data is
            # removed. Neither databases nor token files become test artifacts.
            try:
                h.close()
            finally:
                report["checks"] = h.checks
                if not report["passed"]:
                    diagnostics = args.report.with_suffix(".diagnostics.txt")
                    logs = "\n".join(p.name + "\n" + p.read_text(errors="replace") for p in h.logs)
                    for token in h.secrets:
                        logs = logs.replace(token, "[REDACTED]")
                    diagnostics.write_text(logs, encoding="utf-8")
                    report["diagnostics_file"] = diagnostics.name
                args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("PASS all " + str(len(h.checks)) + " real SIGKILL recovery groups", flush=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("FAIL " + type(error).__name__ + ": " + str(error), file=sys.stderr)
        sys.exit(1)
