#!/usr/bin/env python3
"""Real-binary FOLIO migration/proposal/scheduling release gate (Python stdlib only).

Run after building: python3 scripts/workflows_e2e.py --binary dist/folio
Uses disposable databases, loopback ephemeral ports, HTTP, CLI, and actual MCP
subprocesses. It never accesses an existing instance, external service, or Mac.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

sys.dont_write_bytecode = True

from e2e import Harness, MCP, encode, require


CONTENT_FIELDS = ("title", "slug", "excerpt", "markdown", "tags", "category", "cover", "featured")


def content(post):
    return {key: copy.deepcopy(post[key]) for key in CONTENT_FIELDS}


def timestamp(seconds):
    return datetime.fromtimestamp(seconds, timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def wait_until(seconds):
    while time.time() < seconds:
        time.sleep(max(0, min(0.1, seconds - time.time())))


class WorkflowHarness(Harness):
    def __init__(self, binary, root):
        super().__init__(binary, root)
        self.roles = {}

    def start(self, name, initialize=True, pause=False):
        data = self.root / name
        if initialize:
            initialized = self.cli(["init", "--data", str(data)])
            require(initialized.get("initialized") is True, "Workflow initialization failed")
        token = (data / "token").read_text().strip()
        if name not in self.roles:
            self.roles[name] = {key: secrets.token_hex(32) for key in ("read", "draft", "proposal")}
            self.secrets.extend([token, *self.roles[name].values()])
        roles = self.roles[name]
        env = self.base_env | {"FOLIO_READ_TOKEN": roles["read"], "FOLIO_DRAFT_TOKEN": roles["draft"],
                               "FOLIO_PROPOSAL_TOKEN": roles["proposal"]}
        logpath = self.root / (name + "-" + secrets.token_hex(3) + ".log")
        self.logs.append(logpath)
        command = [self.binary, "serve", "--data", str(data), "--addr", "127.0.0.1:0"]
        if pause:
            command.append("--pause-schedules")
        with logpath.open("wb") as logfile:
            proc = subprocess.Popen(command, env=env, stdin=subprocess.DEVNULL, stdout=logfile, stderr=logfile)
        self.processes.append(proc)
        deadline = time.monotonic() + 15
        url = None
        while time.monotonic() < deadline:
            require(proc.poll() is None, "Workflow daemon exited during startup")
            match = re.search(r"(http://127\.0\.0\.1:\d+)", logpath.read_text())
            if match:
                url = match.group(1)
                break
            time.sleep(0.03)
        require(url is not None, "Workflow daemon did not announce its loopback port")
        server = {"proc": proc, "url": url, "token": token, **roles}
        self.cli(["healthcheck"], server, token="")
        info = self.call(server, "system.info")["data"]
        require(info["scheduler"]["paused"] is pause, "Scheduler pause state disagrees with serve flag")
        return server

    def operation(self, server, operation, args=None, token=None, status=200):
        request = urllib.request.Request(
            server["url"] + "/api/op/" + operation, data=encode({} if args is None else args), method="POST",
            headers={"Content-Type": "application/json", "Accept-Language": "en",
                     "Authorization": "Bearer " + (server["token"] if token is None else token)})
        try:
            response = urllib.request.urlopen(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read()
            self.transcripts.append(body)
            require(response.status == status, "Unexpected authenticated HTTP status for " + operation)
            result = json.loads(body)
            require(result.get("ok") is (status == 200), "HTTP status and operation envelope disagree")
            return result

    def await_schedule(self, server, schedule_id, wanted, timeout=8):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            schedule = self.operation(server, "schedules.get", {"id": schedule_id})["data"]["schedule"]
            if schedule["status"] == wanted:
                return schedule
            require(schedule["status"] == "pending", "Schedule entered unexpected terminal state")
            time.sleep(0.1)
        raise AssertionError("Scheduler did not reach " + wanted + " before readiness deadline")

    def assert_private(self, server, post, marker):
        posts = json.loads(self.http(server, "/api/public/site"))["posts"]
        require(not any(p["id"] == post["id"] for p in posts), "Private post leaked through public site")
        require(not json.loads(self.http(server, "/api/public/search?q=" + urllib.parse.quote(marker)))["posts"],
                "Private text leaked through public search")
        self.http(server, "/api/public/posts/" + post["slug"], 404)
        for path in ("/rss.xml", "/sitemap.xml", "/"):
            public = self.http(server, path).decode()
            require(marker not in public and "/posts/" + post["slug"] not in public,
                    "Private content/URL leaked through " + path)

    def audit_once(self, server, post_id, schedule_id):
        events = self.call(server, "audit.list")["data"]["events"]
        for operation, target in (("posts.publish", post_id), ("schedules.publish", schedule_id)):
            matches = [event for event in events if event["operation"] == operation
                       and event["target"] == target and event["actor"] == "scheduler"]
            require(len(matches) == 1, "Scheduled transition was missing or duplicated in audit")


def migration_request(preview, **overrides):
    return {"plan": preview["plan"], "target_instance_id": preview["target_instance_id"],
            "confirm": True, **overrides}


def imported_file(path, slug, body):
    return {"path": path, "content": "---\ntitle: Imported " + slug + "\nslug: " + slug + "\n---\n" + body}


def bilingual_baseline(h, server, mcp):
    body = "# 双语原文\n\nWORKFLOW_BASELINE_PRIVATE 中文原文。\n\nOriginal English body.\n"
    source = {"path": "旧博客/中文原文.md", "content":
        "---\ntitle: 双语原文 / Bilingual baseline\nslug: workflow-bilingual-baseline\n"
        "description: 原始摘录，保持中英文。 Original excerpt.\ntags: [\"中文\", \"English\"]\n"
        "category: 工作流 / Workflows\ndate: 2020-01-02\npublishDate: 2020-02-03T09:00:00+08:00\n"
        "draft: false\nlegacy_id: WORKFLOW_SOURCE_PRIVATE\n---\n" + body}
    before = h.call(server, "system.info")["data"]["revision"]
    preview = h.call(server, "migration.plan", {"files": [source]})["data"]
    require(preview["can_apply"] and preview["summary"] == {"create": 1, "update": 0, "skip": 0, "blocked": 0},
            "Chinese Markdown did not produce an applicable create plan")
    require(preview == mcp.call("migration.plan", {"files": [source]})["data"],
            "CLI/MCP migration preview is not deterministic")
    require(h.call(server, "system.info")["data"]["revision"] == before
            and h.call(server, "posts.list")["data"]["posts"] == [], "Migration preview wrote content")
    result = mcp.call("migration.apply", migration_request(preview))["data"]
    require(result["report"]["complete"] and result["report"]["outcomes"][0]["status"] == "applied",
            "Migration did not import the Chinese baseline")
    post_id = result["report"]["outcomes"][0]["post_id"]
    original = h.call(server, "posts.get", {"id": post_id})["data"]["post"]
    require(original["markdown"] == body and original["status"] == "draft" and original["revision"] == 1
            and not original.get("published_at") and not original["created_at"].startswith("2020-"),
            "Import changed source text, inherited historical timestamps, or published draft:false")
    before = h.call(server, "system.info")["data"]["revision"]
    require(h.call(server, "migration.apply", migration_request(preview), use_stdin=True)["data"] == result,
            "MCP-to-CLI frozen-plan retry lost the original import receipt")
    require(h.call(server, "system.info")["data"]["revision"] == before, "Import retry duplicated a write")
    exported = h.call(server, "migration.export", {"ids": [post_id]})["data"]
    require(exported == mcp.call("migration.export", {"ids": [post_id]})["data"], "CLI/MCP portable exports differ")
    file, item = exported["files"][0], exported["manifest"]["items"][0]
    encoded = file["content"].encode()
    require(file["bytes"] == len(encoded) and file["sha256"] == hashlib.sha256(encoded).hexdigest()
            and file["sha256"] == item["sha256"] and "draft: true\n" in file["content"]
            and file["content"].endswith(body), "Export lost exact Chinese body or checksum/byte parity")
    require(item["source"]["source_path"] == source["path"] and not item["source"]["draft"]
            and item["source"]["date"] == "2020-01-02T00:00:00Z"
            and item["source"]["publish_date"] == "2020-02-03T01:00:00Z"
            and item["source"]["extra"]["legacy_id"] == "WORKFLOW_SOURCE_PRIVATE",
            "Portable manifest lost historical/private source metadata")
    h.assert_private(server, original, "WORKFLOW_BASELINE_PRIVATE")
    h.check("real CLI plan -> MCP apply imports Chinese Markdown privately; frozen retry and checksummed export preserve exact source")
    return original


def migration_safety(h):
    server = h.start("migration-safety")
    mcp = MCP(h, server, locale="en")
    files = [imported_file(name + ".md", "migration-" + name, "迁移 " + name + "\n") for name in ("a", "b", "c")]
    preview = mcp.call("migration.plan", {"files": files})["data"]
    request = migration_request(preview)
    before = h.call(server, "system.info")["data"]["revision"]
    for role in ("read", "draft", "proposal"):
        scoped_mcp = MCP(h, server | {"token": server[role]}, locale="en")
        for operation, args in (("migration.plan", {"files": files}), ("migration.apply", request),
                                ("migration.export", {})):
            denied = h.operation(server, operation, args, server[role], 403)
            require(denied["error"]["code"] == "forbidden", "Non-owner migration HTTP permission changed")
            for result in (h.call(server, operation, args, token=server[role], expect_ok=False),
                           scoped_mcp.call(operation, args, expect_ok=False)):
                require(result["error"]["code"] == "authentication_failed" and result["error"]["http_status"] == 403,
                        "CLI/MCP migration scope escaped owner restriction")
        scoped_mcp.close()
    unconfirmed = h.operation(server, "migration.apply", migration_request(preview, confirm=False), status=400)
    require(unconfirmed["error"]["code"] == "confirmation_required", "Migration accepted missing review confirmation")
    foreign = h.start("migration-foreign")
    rejected = h.operation(foreign, "migration.apply", request, status=409)
    require(rejected["error"]["code"] == "conflict" and not h.call(foreign, "posts.list")["data"]["posts"],
            "Migration plan wrote to a different target instance")
    h.stop(foreign["proc"])
    tampered = copy.deepcopy(request)
    tampered["plan"]["entries"][0]["document"]["markdown"] += "UNREVIEWED"
    require(h.operation(server, "migration.apply", tampered, status=400)["error"]["code"] == "validation",
            "Migration applied a mutated frozen plan")
    mixed = h.call(server, "migration.plan", {"files": files + [{"path": "../escape.md", "content": "# Escape"}]})["data"]
    require(not mixed["can_apply"] and mixed["validation_issues"] and mixed["plan"]["diagnostics"],
            "Rejected source diagnostics are not frozen into plan")
    require(h.operation(server, "migration.apply", migration_request(mixed), status=400)["error"]["code"] == "validation",
            "Rejected migration preview applied its good subset")
    require(h.call(server, "system.info")["data"]["revision"] == before, "Rejected migrations changed instance")
    h.check("migration HTTP/CLI/MCP owner scope, explicit confirmation, exact target, plan tamper and mixed-invalid-batch gates")

    collision = h.call(server, "posts.create", {"title": "Concurrent owner", "slug": "migration-b", "markdown": "OWNER_PRIVATE"})["data"]["post"]
    partial = mcp.call("migration.apply", request)["data"]["report"]
    require(not partial["complete"] and [row["status"] for row in partial["outcomes"]] == ["applied", "failed", "pending"],
            "Default partial migration did not stop after first failed write")
    first_id = partial["outcomes"][0]["post_id"]
    mcp.close()
    h.stop(server["proc"])
    server = h.start("migration-safety", initialize=False)
    continued = h.call(server, "migration.apply", migration_request(preview, continue_on_error=True))["data"]["report"]
    require(not continued["complete"] and [row["status"] for row in continued["outcomes"]] == ["applied", "failed", "applied"]
            and continued["outcomes"][0]["post_id"] == first_id, "Restart/continue lost receipt or skipped later write")
    third_id = continued["outcomes"][2]["post_id"]
    h.call(server, "posts.delete", {"id": collision["id"], "expected_revision": collision["revision"], "confirm": True})
    mcp = MCP(h, server, locale="zh-CN")
    completed = mcp.call("migration.apply", request)["data"]["report"]
    require(completed["complete"] and all(row["status"] == "applied" for row in completed["outcomes"])
            and completed["outcomes"][0]["post_id"] == first_id and completed["outcomes"][2]["post_id"] == third_id,
            "Resolved collision retry failed to preserve successful rows")
    for row in completed["outcomes"]:
        record = h.call(server, "posts.get", {"id": row["post_id"]})["data"]
        require(record["post"]["revision"] == 1 and len(record["revisions"]) == 1, "Migration retry duplicated content history")
    before = h.call(server, "system.info")["data"]["revision"]
    require(h.call(server, "migration.apply", request)["data"]["report"] == completed
            and h.call(server, "system.info")["data"]["revision"] == before, "Completed retry was not a no-op")
    skipped = mcp.call("migration.plan", {"files": files, "conflict": "skip"})["data"]
    skip_report = mcp.call("migration.apply", migration_request(skipped))["data"]["report"]
    require(skip_report["complete"] and all(row["status"] == "skipped" for row in skip_report["outcomes"])
            and h.call(server, "system.info")["data"]["revision"] == before, "Skip conflict policy wrote content")
    h.check("migration partial successes survive restart; stop/continue reports, resolved-conflict retries and skip preserve exact receipts")

    first = h.call(server, "posts.get", {"id": first_id})["data"]["post"]
    replacement = imported_file("a.md", first["slug"], "REVIEWED_REPLACEMENT 中文\n")
    update_plan = mcp.call("migration.plan", {"files": [replacement], "conflict": "update"})["data"]
    owner = h.call(server, "posts.update", content(first) | {"id": first_id, "expected_revision": first["revision"],
                                                             "markdown": "NEWER_OWNER_PRIVATE"})["data"]["post"]
    before = h.call(server, "system.info")["data"]["revision"]
    stale = mcp.call("migration.apply", migration_request(update_plan))["data"]["report"]
    require(not stale["complete"] and stale["outcomes"][0]["status"] == "failed"
            and h.call(server, "posts.get", {"id": first_id})["data"]["post"] == owner
            and h.call(server, "system.info")["data"]["revision"] == before, "Stale migration update clobbered owner draft")
    fresh = h.call(server, "migration.plan", {"files": [replacement], "conflict": "update"})["data"]
    updated = mcp.call("migration.apply", migration_request(fresh))["data"]["report"]
    require(updated["complete"] and updated["outcomes"][0]["post_id"] == first_id
            and updated["outcomes"][0]["revision"] == owner["revision"] + 1, "Fresh update did not apply exact target CAS")
    h.check("migration replacement rejects stale draft CAS and only a freshly reviewed plan advances the original post")
    mcp.close()
    h.stop(server["proc"])


def workflow_lifecycle(h):
    server = h.start("workflows")
    admin_mcp = MCP(h, server, locale="en")
    proposer_mcp = MCP(h, server | {"token": server["proposal"]}, locale="zh-CN")
    discovered = h.cli(["capabilities"], server, token="")["data"]["operations"]
    opmap = {operation["name"]: operation for operation in discovered}
    toolmap = {tool["name"]: tool for tool in proposer_mcp.request("tools/list", {})["tools"]}
    workflow_names = {"proposals.create", "proposals.list", "proposals.get", "proposals.approve", "proposals.reject",
                      "proposals.cancel", "schedules.create", "schedules.list", "schedules.get",
                      "schedules.cancel", "schedules.reschedule", "migration.plan", "migration.apply", "migration.export"}
    require(workflow_names <= opmap.keys(), "Workflow operations missing from discovery")
    owner_toolmap = {tool["name"]: tool for tool in admin_mcp.request("tools/list", {})["tools"]}
    require(len(toolmap) == len(opmap) == len(owner_toolmap), "CLI/HTTP and owner/proposer MCP operation counts differ")
    for name in opmap:
        for tools in (toolmap, owner_toolmap):
            require(tools[name.replace(".", "_")]["inputSchema"] == opmap[name]["input_schema"],
                    "MCP and HTTP schema mismatch for " + name)
    h.check("all " + str(len(opmap)) + " CLI/HTTP/owner-MCP/proposer-MCP operation schemas match exactly, including migration/workflows")

    original = bilingual_baseline(h, server, admin_mcp)
    proposer_mcp.call("posts.get", {"id": original["id"]})
    candidate = content(original) | {"title": "  第一份提案 / First proposal  ",
        "markdown": "WORKFLOW_FIRST_PRIVATE 第一份提案。\n\nOriginal text is preserved.\n"}
    request = {"post_id": original["id"], "base_revision": original["revision"], "candidate": candidate,
               "idempotency_key": "actor-isolated-proposal"}
    proposal = proposer_mcp.call("proposals.create", request)["data"]["proposal"]
    require(proposal["candidate"] == candidate and proposal["base"] == content(original), "Proposal altered authored content")
    retry = h.call(server, "proposals.create", request, token=server["proposal"])["data"]["proposal"]
    require(retry["id"] == proposal["id"], "MCP-to-CLI identical retry duplicated proposal")
    owner_proposal = h.call(server, "proposals.create", request)["data"]["proposal"]
    require(owner_proposal["id"] != proposal["id"] and owner_proposal["created_by"] == "admin", "Retry cache leaked across actors")
    own_list = proposer_mcp.call("proposals.list")["data"]["proposals"]
    require([item["id"] for item in own_list] == [proposal["id"]], "Proposal listing leaked another actor's candidate")
    hidden = h.operation(server, "proposals.get", {"id": owner_proposal["id"]}, server["proposal"], 404)
    require(hidden["error"]["code"] == "not_found", "Foreign proposal read did not hide existence")
    h.assert_private(server, original, "WORKFLOW_BASELINE_PRIVATE")
    unchanged = h.call(server, "posts.get", {"id": original["id"]})["data"]
    require(content(unchanged["post"]) == content(original) and len(unchanged["revisions"]) == 1,
            "Proposal submission changed baseline draft/history")
    h.check("immutable bilingual proposal, private baseline, actor-isolated retries and ownership filtering")

    denied = {"id": proposal["id"], "expected_revision": proposal["revision"], "confirm": True}
    blocked = {
        "proposals.approve": denied, "proposals.reject": denied,
        "posts.create": candidate, "posts.update": content(original) | {"id": original["id"], "expected_revision": original["revision"]},
        "posts.restore": {"id": original["id"], "expected_revision": original["revision"], "revision": 1},
        "posts.publish": {"id": original["id"], "expected_revision": original["revision"], "confirm": True},
        "posts.unpublish": {}, "posts.delete": {}, "posts.recover": {}, "media.upload": {},
        "settings.update": {}, "audit.list": {}, "backup.export": {}, "backup.restore": {},
        "schedules.create": {"post_id": original["id"], "expected_revision": original["revision"],
                             "publish_at": timestamp(time.time() + 60), "confirm": True},
        "schedules.list": {}, "schedules.get": {}, "schedules.cancel": {}, "schedules.reschedule": {},
    }
    for operation, args in blocked.items():
        result = h.operation(server, operation, args, server["proposal"], 403)
        require(result["error"]["code"] == "forbidden", "Proposal token received unexpected denial code")
    mcp_denied = proposer_mcp.call("proposals.approve", denied, expect_ok=False)
    require(mcp_denied["error"]["code"] == "authentication_failed" and mcp_denied["error"]["http_status"] == 403,
            "MCP proposal token escaped owner-only review")
    cli_denied = h.call(server, "posts.publish", blocked["posts.publish"], token=server["proposal"], expect_ok=False)
    require(cli_denied["error"]["code"] == "authentication_failed" and cli_denied["error"]["http_status"] == 403,
            "CLI proposal token escaped publish restriction")
    info = h.call(server, "system.info", token=server["proposal"])["data"]
    require(info["actor"] == "proposal" and not (set(blocked) & set(info["permissions"])), "Reported proposer permissions are overbroad")
    h.check("proposal-only HTTP/CLI/MCP scope cannot approve, write drafts, publish, schedule or administer")

    owner_edit = content(original) | {"title": "所有者修订 / Owner edit", "markdown": "WORKFLOW_OWNER_PRIVATE 所有者的新内容。\n"}
    current = h.call(server, "posts.update", owner_edit | {"id": original["id"], "expected_revision": original["revision"]})["data"]["post"]
    before = h.call(server, "system.info")["data"]["revision"]
    stale = h.operation(server, "proposals.approve", denied, status=409)
    require(stale["error"]["code"] == "conflict", "Stale approval did not report CAS conflict")
    after = h.call(server, "posts.get", {"id": original["id"]})["data"]["post"]
    require(after == current and h.call(server, "system.info")["data"]["revision"] == before,
            "Stale approval overwrote owner work or changed instance revision")
    require(proposer_mcp.call("proposals.get", {"id": proposal["id"]})["data"]["proposal"]["status"] == "pending",
            "Stale approval closed the proposal")
    proposer_mcp.call("proposals.cancel", denied)
    h.call(server, "proposals.cancel", {"id": owner_proposal["id"], "expected_revision": 1, "confirm": True})
    reviewed = content(current) | {"title": "  已审阅 WORKFLOW_APPROVED_PUBLIC  ", "slug": "workflow-approved-public",
        "excerpt": "已经审阅的中英文摘录 / Reviewed excerpt", "tags": ["已审阅", "reviewed"],
        "markdown": "# 已审阅正文\n\nWORKFLOW_APPROVED_PUBLIC 原文不会被改写。\n\nExactly this approved revision.\n"}
    refreshed = proposer_mcp.call("proposals.create", {"post_id": current["id"], "base_revision": current["revision"],
                                                       "candidate": reviewed})["data"]["proposal"]
    approved_result = admin_mcp.call("proposals.approve", {"id": refreshed["id"], "expected_revision": 1, "confirm": True})["data"]
    approved = approved_result["post"]
    require(content(approved) == reviewed and approved["revision"] == current["revision"] + 1 and approved["status"] == "draft",
            "Owner approval did not create the exact private draft revision")
    h.assert_private(server, approved, "WORKFLOW_APPROVED_PUBLIC")
    h.check("HTTP 409 stale-base approval leaves owner edit intact; fresh owner approval creates only a private draft")

    due = time.time() + 6
    schedule_args = {"post_id": approved["id"], "expected_revision": approved["revision"], "publish_at": timestamp(due),
                     "confirm": True, "idempotency_key": "pin-approved-once"}
    schedule = admin_mcp.call("schedules.create", schedule_args)["data"]["schedule"]
    require(schedule["post_revision"] == approved["revision"] and content(schedule["snapshot"]) == reviewed,
            "Schedule did not pin the complete reviewed revision")
    newer_content = content(approved) | {"title": "WORKFLOW_NEWER_PRIVATE 后续草稿", "slug": "workflow-newer-private",
        "excerpt": "WORKFLOW_NEWER_PRIVATE 后续摘录", "tags": ["WORKFLOW_NEWER_PRIVATE"],
        "markdown": "WORKFLOW_NEWER_PRIVATE 这份更新的内容不能自动发布。\n\nNew private draft.\n"}
    newer = h.call(server, "posts.update", newer_content | {"id": approved["id"], "expected_revision": approved["revision"]})["data"]["post"]
    h.assert_private(server, approved, "WORKFLOW_APPROVED_PUBLIC")
    h.assert_private(server, newer, "WORKFLOW_NEWER_PRIVATE")
    admin_mcp.close()
    proposer_mcp.close()
    h.stop(server["proc"])
    require(time.time() < due, "Fixture setup exceeded future deadline before daemon stopped")
    wait_until(due + 0.1)
    server = h.start("workflows", initialize=False)
    terminal = h.await_schedule(server, schedule["id"], "published")
    require(terminal["revision"] == 2 and terminal["executed_at"], "Missing durable schedule execution outcome")
    live = json.loads(h.http(server, "/api/public/posts/" + approved["slug"]))["post"]
    require(content(live) == reviewed and live["revision"] == approved["revision"], "Restart published the wrong content revision")
    draft = h.call(server, "posts.get", {"id": approved["id"]})["data"]["post"]
    require(content(draft) == newer_content and draft["revision"] == newer["revision"] and draft["updated_at"] == newer["updated_at"]
            and draft["status"] == "changed" and draft["published_revision"] == approved["revision"],
            "Scheduled activation changed or exposed the newer private head")
    search = json.loads(h.http(server, "/api/public/search?q=WORKFLOW_APPROVED_PUBLIC"))["posts"]
    require(len(search) == 1 and search[0]["revision"] == approved["revision"], "Public search missed approved snapshot")
    require(not json.loads(h.http(server, "/api/public/search?q=WORKFLOW_NEWER_PRIVATE"))["posts"], "Newer private content leaked through search")
    h.http(server, "/api/public/posts/" + newer["slug"], 404)
    for path in ("/api/public/site", "/rss.xml", "/sitemap.xml", "/", "/posts/" + approved["slug"]):
        body = h.http(server, path).decode()
        require("WORKFLOW_NEWER_PRIVATE" not in body and newer["slug"] not in body
                and "WORKFLOW_SOURCE_PRIVATE" not in body and "source_path" not in body,
                "Newer private metadata or migration provenance leaked through " + path)
        if path != "/sitemap.xml":
            require("WORKFLOW_APPROVED_PUBLIC" in body, "Approved content missing from " + path)
    h.audit_once(server, approved["id"], schedule["id"])
    time.sleep(2.2)  # Cross two additional worker ticks to prove terminal jobs stay terminal.
    h.audit_once(server, approved["id"], schedule["id"])
    h.stop(server["proc"])
    server = h.start("workflows", initialize=False)
    require(h.call(server, "schedules.get", {"id": schedule["id"]})["data"]["schedule"] == terminal,
            "Terminal schedule changed on second restart")
    h.audit_once(server, approved["id"], schedule["id"])
    h.check("overdue restart catch-up publishes pinned revision exactly once; newer draft stays private on every public surface")
    h.stop(server["proc"])


def paused_recovery(h):
    source = h.start("paused-source", pause=True)
    post = h.call(source, "posts.create", {"title": "恢复前复查 WORKFLOW_RECOVERY_PRIVATE", "slug": "workflow-recovery",
        "markdown": "WORKFLOW_RECOVERY_PRIVATE 先暂停，再审阅。\n\nReview before recovery publication.\n"})["data"]["post"]
    due = time.time() + 3
    schedule = h.call(source, "schedules.create", {"post_id": post["id"], "expected_revision": post["revision"],
        "publish_at": timestamp(due), "confirm": True})["data"]["schedule"]
    backup = h.call(source, "backup.export")["data"]
    require(backup["state"]["schedules"][schedule["id"]]["status"] == "pending", "Recovery fixture lost pending job")
    h.stop(source["proc"])
    wait_until(due + 0.1)
    target = h.start("paused-recovery", pause=True)
    h.call(target, "backup.restore", {"backup": backup, "confirm": True}, use_stdin=True)
    time.sleep(2.2)
    pending = h.call(target, "schedules.get", {"id": schedule["id"]})["data"]["schedule"]
    require(pending == schedule, "Paused restore changed or executed overdue schedule")
    h.assert_private(target, post, "WORKFLOW_RECOVERY_PRIVATE")
    require(not any(event["actor"] == "scheduler" for event in h.call(target, "audit.list")["data"]["events"]),
            "Paused recovery still ran scheduler")
    h.stop(target["proc"])
    target = h.start("paused-recovery", initialize=False, pause=True)
    h.assert_private(target, post, "WORKFLOW_RECOVERY_PRIVATE")
    require(h.call(target, "schedules.get", {"id": schedule["id"]})["data"]["schedule"]["status"] == "pending",
            "Repeated paused restart published overdue content")
    h.stop(target["proc"])
    target = h.start("paused-recovery", initialize=False)
    h.await_schedule(target, schedule["id"], "published")
    live = json.loads(h.http(target, "/api/public/posts/" + post["slug"]))["post"]
    require(content(live) == content(post), "Unpaused recovery published a changed snapshot")
    h.audit_once(target, post["id"], schedule["id"])
    h.check("--pause-schedules protects overdue backup restore across restarts until explicitly restarted without pause")
    h.stop(target["proc"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default="dist/folio")
    args = parser.parse_args()
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error("Build the FOLIO binary first: go build -buildvcs=false -o dist/folio ./cmd/folio")
    with tempfile.TemporaryDirectory(prefix="folio-workflows-e2e-") as temporary:
        harness = WorkflowHarness(binary, Path(temporary))
        try:
            workflow_lifecycle(harness)
            migration_safety(harness)
            paused_recovery(harness)
            harness.check_no_secrets()
            harness.check("no owner/read/draft/proposal token values in captured protocol output or logs")
        finally:
            harness.close()
        print("PASS all " + str(len(harness.checks)) + " real-binary workflow groups")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("FAIL " + type(error).__name__ + ": " + str(error), file=sys.stderr)
        sys.exit(1)
