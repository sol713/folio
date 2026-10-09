#!/usr/bin/env python3
"""Real-binary FOLIO lifecycle test, using only the Python standard library.

Run after building: python3 scripts/e2e.py --binary dist/folio
All servers, tokens and databases are disposable. No existing instance is used.
The process owns its HTTP servers and MCP child, avoiding cross-session networking.
"""
from __future__ import annotations
import argparse
import base64
import copy
import json
import http.client
import os
from pathlib import Path
import re
import secrets
import selectors
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import urllib.parse


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def encode(value):
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False).encode()


class Harness:
    def __init__(self, binary, root):
        self.binary = str(binary)
        self.root = root
        self.base_env = {k: v for k, v in os.environ.items() if not k.startswith("FOLIO_")}
        self.secrets = []
        self.transcripts = []
        self.logs = []
        self.processes = []
        self.checks = []

    def env(self, server=None, token=None):
        env = self.base_env.copy()
        if server:
            env["FOLIO_URL"] = server["url"]
            env["FOLIO_TOKEN"] = server["token"] if token is None else token
        return env

    def cli(self, args, server=None, token=None, expect_ok=True, stdin=None):
        run = subprocess.run([self.binary, *args], env=self.env(server, token),
                             input=stdin, capture_output=True, timeout=15)
        self.transcripts.extend([run.stdout, run.stderr])
        
        try:
            result = json.loads(run.stdout)
        except (json.JSONDecodeError, UnicodeDecodeError):
            raise AssertionError("CLI stdout must contain exactly one JSON value") from None
        require((run.returncode == 0) == expect_ok, "CLI exit mismatch: " + " ".join(args[:2]) + " expected_success=" + str(expect_ok) + " error_code=" + str(result.get("error", {}).get("code", "none")))
        if "ok" in result:
            require(result["ok"] == expect_ok, "CLI JSON success flag disagrees with exit code")
        return result

    def call(self, server, op, args=None, expect_ok=True, token=None, use_stdin=False):
        args = {} if args is None else args
        if use_stdin:
            return self.cli(["call", op, "--file", "-"], server, token, expect_ok, encode(args))
        return self.cli(["call", op, "--json", encode(args).decode()], server, token, expect_ok)

    def start(self, name, initialize=True):
        data = self.root / name
        if initialize:
            initialized = self.cli(["init", "--data", str(data)])
            require(initialized.get("initialized") is True, "Initialization failed")
        token = (data / "token").read_text().strip()
        read_token, draft_token = secrets.token_hex(32), secrets.token_hex(32)
        self.secrets.extend([token, read_token, draft_token])
        env = self.base_env | {"FOLIO_READ_TOKEN": read_token, "FOLIO_DRAFT_TOKEN": draft_token}
        logpath = self.root / (name + "-" + secrets.token_hex(3) + ".log")
        logfile = logpath.open("wb")
        self.logs.append(logpath)
        proc = subprocess.Popen([self.binary, "serve", "--data", str(data), "--addr", "127.0.0.1:0"],
                                env=env, stdin=subprocess.DEVNULL, stdout=logfile, stderr=logfile)
        logfile.close()
        self.processes.append(proc)
        deadline = time.monotonic() + 15
        url = None
        while time.monotonic() < deadline:
            require(proc.poll() is None, "Daemon exited during startup")
            match = re.search(r"(http://127\.0\.0\.1:\d+)", logpath.read_text())
            if match:
                url = match.group(1)
                break
            time.sleep(0.03)
        require(url is not None, "Daemon did not announce its bound loopback port")
        server = {"proc": proc, "url": url, "token": token, "read": read_token, "draft": draft_token}
        self.cli(["healthcheck"], server, token="")
        return server

    def http(self, server, path, status=200):
        request = urllib.request.Request(server["url"] + path)
        try:
            response = urllib.request.urlopen(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read()
            require(response.status == status, "Unexpected public HTTP status for " + path)
            self.transcripts.append(body)
            return body

    def reject_duplicate_roles(self, name, token):
        env = self.base_env | {"FOLIO_READ_TOKEN": token, "FOLIO_DRAFT_TOKEN": token}
        try:
            run = subprocess.run([self.binary, "serve", "--data", str(self.root / name), "--addr", "127.0.0.1:0"],
                                 env=env, capture_output=True, timeout=3)
        except subprocess.TimeoutExpired:
            raise AssertionError("Daemon accepted identical read/draft token values") from None
        self.transcripts.extend([run.stdout, run.stderr])
        require(run.returncode != 0, "Duplicate role tokens should fail startup")

    def origin_request(self, server, origin, status):
        request = urllib.request.Request(server["url"] + "/api/op/posts.list", data=b"{}", method="POST",
                                         headers={"Content-Type": "application/json", "Authorization": "Bearer " + server["token"], "Origin": origin})
        try:
            response = urllib.request.urlopen(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            self.transcripts.append(response.read())
            require(response.status == status, "Origin policy returned unexpected HTTP status")

    def graceful_shutdown(self):
        server = self.start("graceful-shutdown")
        address = urllib.parse.urlsplit(server["url"])
        # Expect: 100-continue confirms that the handler is already reading the
        # body before SIGTERM; a timed sleep alone would be a flaky readiness test.
        with socket.create_connection((address.hostname, address.port), timeout=1) as connection:
            headers = (
                "POST /api/op/system.info HTTP/1.1\r\n"
                "Host: " + address.netloc + "\r\n"
                "Authorization: Bearer " + server["token"] + "\r\n"
                "Content-Type: application/json\r\n"
                "Content-Length: 2\r\n"
                "Expect: 100-continue\r\n"
                "Connection: close\r\n\r\n"
            )
            connection.sendall(headers.encode("ascii"))
            interim = b""
            while b"\r\n\r\n" not in interim:
                chunk = connection.recv(4096)
                require(chunk, "Daemon closed before acknowledging the in-flight request")
                interim += chunk
                require(len(interim) <= 65536, "Unexpected oversized interim HTTP response")
            self.transcripts.append(interim)
            require(interim.startswith(b"HTTP/1.1 100 Continue\r\n"), "Handler did not acknowledge request body readiness")
            connection.sendall(b"{")
            server["proc"].terminate()
            time.sleep(0.2)
            require(server["proc"].poll() is None, "Daemon exited while a request body was still in flight")
            connection.sendall(b"}")
            response = http.client.HTTPResponse(connection)
            response.begin()
            body = response.read()
            self.transcripts.append(body)
            require(response.status == 200, "Graceful shutdown did not drain the in-flight request with HTTP 200")
            require(json.loads(body).get("ok") is True, "In-flight operation failed during graceful shutdown")
            response.close()
        require(server["proc"].wait(timeout=3) == 0, "Daemon did not exit cleanly after draining the request")
        self.check("SIGTERM drains a confirmed in-flight partial HTTP request before clean daemon exit")

    def language_contract(self):
        for locale, marker in [("zh-CN", "用法："), ("en", "Usage:")]:
            run = subprocess.run([self.binary, "--lang", locale, "--help"], env=self.base_env,
                                 capture_output=True, timeout=5)
            self.transcripts.extend([run.stdout, run.stderr])
            require(run.returncode == 0 and not run.stdout and marker.encode() in run.stderr,
                    "CLI help did not use the selected language on stderr")
            result = self.cli(["--lang", locale, "call", "posts.list", "--json", "[]"], expect_ok=False)
            require(result["error"]["code"] == "invalid_json", "Locale changed the machine error code")
            require(("JSON 对象" if locale == "zh-CN" else "JSON object") in result["error"]["message"],
                    "CLI error.message did not use the selected locale")
            require("version" in self.cli(["--lang", locale, "version"]), "Global locale flag broke root dispatch")
        result = self.cli(["--lang", "unsupported", "help"], expect_ok=False)
        require(result["error"]["code"] == "invalid_locale", "Invalid explicit locale was not rejected")
        for env in [self.base_env, self.base_env | {"FOLIO_LANG": "unsupported"}]:
            run = subprocess.run([self.binary, "help"], env=env, capture_output=True, timeout=5)
            self.transcripts.extend([run.stdout, run.stderr])
            require(run.returncode == 0 and "用法：".encode() in run.stderr,
                    "Missing or unsupported environment locale did not default to Chinese")
        self.check("Chinese-default/English CLI help and errors, stable codes, invalid locale and root global flags")

    def check(self, name):
        self.checks.append(name)
        print("PASS " + name, flush=True)

    def stop(self, proc):
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)

    def check_no_secrets(self):
        blobs = self.transcripts + [p.read_bytes() for p in self.logs]
        for token in self.secrets:
            require(not any(token.encode() in blob for blob in blobs),
                    "A token appeared in captured CLI/MCP/public output or daemon logs")

    def close(self):
        for proc in reversed(self.processes):
            self.stop(proc)
        self.check_no_secrets()


class MCP:
    def __init__(self, h, server, locale=None):
        self.h = h
        self.logpath = h.root / ("mcp-" + secrets.token_hex(3) + ".stderr.log")
        logfile = self.logpath.open("wb")
        h.logs.append(self.logpath)
        command = [h.binary, "mcp"] if locale is None else [h.binary, "--lang", locale, "mcp"]
        self.proc = subprocess.Popen(command, env=h.env(server), stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=logfile, bufsize=0)
        logfile.close()
        h.processes.append(self.proc)
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.proc.stdout, selectors.EVENT_READ)
        self.buffer = b""
        self.next_id = 1
        initialized = self.request("initialize", {
            "protocolVersion": "2025-06-18", "capabilities": {},
            "clientInfo": {"name": "folio-real-e2e", "version": "1.0.0"},
        })
        require(initialized.get("serverInfo", {}).get("name") == "folio", "Unexpected MCP server identity")
        self.send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def send(self, message):
        self.proc.stdin.write(encode(message) + b"\n")
        self.proc.stdin.flush()

    def read(self):
        deadline = time.monotonic() + 15
        while b"\n" not in self.buffer:
            remaining = deadline - time.monotonic()
            require(remaining > 0, "MCP response timed out")
            require(self.selector.select(remaining), "MCP response timed out")
            chunk = os.read(self.proc.stdout.fileno(), 65536)
            require(chunk, "MCP stdout closed unexpectedly")
            self.buffer += chunk
        line, self.buffer = self.buffer.split(b"\n", 1)
        self.h.transcripts.append(line)
        try:
            message = json.loads(line)
        except (json.JSONDecodeError, UnicodeDecodeError):
            raise AssertionError("MCP stdout contained non-JSON protocol text") from None
        require(isinstance(message, dict) and message.get("jsonrpc") == "2.0", "MCP stdout contained a non-JSON-RPC value")
        return message

    def request(self, method, params):
        request_id = self.next_id
        self.next_id += 1
        self.send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        while True:
            response = self.read()
            if response.get("id") == request_id:
                require("error" not in response, "Unexpected MCP protocol error for " + method)
                return response["result"]
            require("method" in response and "id" not in response, "Unexpected MCP response ID")

    def call(self, operation, args=None, expect_ok=True):
        result = self.request("tools/call", {"name": operation.replace(".", "_"), "arguments": args or {}})
        require(bool(result.get("isError", False)) != expect_ok, "MCP isError disagrees with expected outcome for " + operation)
        structured = result.get("structuredContent")
        require(isinstance(structured, dict) and structured.get("ok") == expect_ok, "MCP structured envelope missing or incorrect")
        text = [c["text"] for c in result.get("content", []) if c.get("type") == "text"]
        require(text and json.loads(text[0]) == structured, "MCP JSON text fallback differs from structured content")
        return structured

    def close(self):
        self.proc.stdin.close()
        self.proc.wait(timeout=5)
        remainder = self.buffer + self.proc.stdout.read()
        for line in remainder.splitlines():
            self.h.transcripts.append(line)
            require(json.loads(line).get("jsonrpc") == "2.0", "Non-JSON-RPC MCP shutdown stdout")
        require(self.proc.returncode == 0, "MCP exited nonzero on clean stdin close")
        self.selector.close()


def lifecycle(h):
    h.language_contract()
    server = h.start("source")
    h.reject_duplicate_roles("source", server["read"])
    settings = h.call(server, "settings.get")["data"]
    proxy_origin = server["url"].replace("http://", "https://", 1)
    adjusted = settings["settings"] | {"base_url": proxy_origin}
    updated_settings = h.call(server, "settings.update", {"settings": adjusted, "expected_revision": settings["revision"]})["data"]
    h.origin_request(server, proxy_origin, 200)
    h.origin_request(server, proxy_origin + "/unexpected-path", 403)
    h.origin_request(server, "https://untrusted.example", 403)
    h.call(server, "settings.update", {"settings": settings["settings"], "expected_revision": updated_settings["revision"]})
    h.check("duplicate role-token rejection and configured HTTPS reverse-proxy Origin policy")
    discovery = h.cli(["capabilities"], server, token="")["data"]
    require(discovery.get("operations"), "No discovered operations")
    mcp = MCP(h, server)
    tools = mcp.request("tools/list", {})["tools"]
    toolmap = {tool["name"]: tool for tool in tools}
    english_mcp = MCP(h, server, locale="en")
    english_tools = {tool["name"]: tool for tool in english_mcp.request("tools/list", {})["tools"]}
    require(toolmap == english_tools, "MCP tool metadata or schemas changed with locale")
    for session, marker in [(mcp, "JSON 对象"), (english_mcp, "JSON object")]:
        failure = session.request("tools/call", {"name": "posts_list", "arguments": []})
        envelope = failure.get("structuredContent", {})
        require(failure.get("isError") is True and envelope.get("error", {}).get("code") == "invalid_json",
                "MCP language changed the structured error contract")
        require(marker in envelope["error"]["message"], "MCP error was not localized")
    english_mcp.close()
    h.check("canonical MCP tools/schemas across zh-CN/en with localized human errors")
    baseline_operations = {
        "system.capabilities", "system.info", "posts.list", "posts.get", "posts.create",
        "posts.update", "posts.preview", "posts.publish", "posts.unpublish", "posts.delete",
        "posts.recover", "posts.restore", "settings.get", "settings.update", "media.list",
        "media.upload", "audit.list", "backup.export", "backup.restore",
    }
    operation_names = {operation["name"] for operation in discovery["operations"]}
    require(baseline_operations <= operation_names, "A baseline operation disappeared from discovery")
    require(len(tools) == len(discovery["operations"]), "MCP/HTTP operation counts differ")
    for op in discovery["operations"]:
        tool = toolmap[op["name"].replace(".", "_")]
        require(tool["inputSchema"] == op["input_schema"], "MCP input schema mismatch for " + op["name"])
        require(tool["annotations"]["readOnlyHint"] == op["read_only"], "MCP read-only hint mismatch")
    h.check("healthcheck, public discovery, real MCP initialize and exact schema parity (" + str(len(tools)) + " tools)")

    original = "# Lifecycle test\n\nE2E published content, version one."
    create = {"title": "E2E lifecycle", "slug": "e2e-lifecycle", "markdown": original,
              "tags": ["testing"], "idempotency_key": "e2e-create-one"}
    post = mcp.call("posts.create", create)["data"]["post"]
    post_id, revision = post["id"], post["revision"]
    require(post["status"] == "draft", "Create unexpectedly published content")
    retry = h.call(server, "posts.create", create)["data"]["post"]
    require(retry["id"] == post_id, "Identical idempotent retry created another post")
    before = h.call(server, "system.info")["data"]["revision"]
    preview = mcp.call("posts.preview", {"id": post_id, "revision": revision})["data"]
    require("<h1>Lifecycle test</h1>" in preview["html"], "Preview failed to render Markdown")
    require(h.call(server, "system.info")["data"]["revision"] == before, "Preview mutated state")
    h.http(server, "/api/public/posts/e2e-lifecycle", 404)
    failed = mcp.call("posts.publish", {"id": post_id, "expected_revision": revision}, False)
    require(failed["error"]["code"] == "confirmation_required", "Publish did not enforce confirmation")
    h.http(server, "/api/public/posts/e2e-lifecycle", 404)
    published = h.call(server, "posts.publish", {"id": post_id, "expected_revision": revision, "confirm": True})["data"]["post"]
    public = json.loads(h.http(server, "/api/public/posts/e2e-lifecycle"))["post"]
    require(public["markdown"] == original and published["status"] == "published", "Published snapshot differs from reviewed draft")
    h.check("MCP draft creation, CLI idempotent retry, read-only preview, confirmation guard and public publish")

    changed = "# Lifecycle test\n\nE2E private draft, version two."
    update = {"id": post_id, "expected_revision": revision, "title": "E2E edited draft", "slug": "e2e-lifecycle", "markdown": changed}
    edited = mcp.call("posts.update", update)["data"]["post"]
    require(edited["revision"] > revision and edited["status"] == "changed", "Draft edit did not advance revision")
    require(json.loads(h.http(server, "/api/public/posts/e2e-lifecycle"))["post"]["markdown"] == original, "Private edit leaked into live snapshot")
    stale = h.call(server, "posts.update", update, expect_ok=False)
    require(stale["error"]["code"] == "conflict", "Stale update was not rejected")
    stale = mcp.call("posts.publish", {"id": post_id, "expected_revision": revision, "confirm": True}, False)
    require(stale["error"]["code"] == "conflict", "Stale publish was not rejected")
    revision = edited["revision"]
    h.call(server, "posts.get", {"id": post_id}, token=server["read"])
    h.call(server, "posts.update", update | {"expected_revision": revision}, token=server["read"], expect_ok=False)
    h.call(server, "posts.preview", {"id": post_id}, token=server["draft"])
    h.call(server, "posts.publish", {"id": post_id, "expected_revision": revision, "confirm": True}, token=server["draft"], expect_ok=False)
    wrong_token = secrets.token_hex(32)
    h.secrets.append(wrong_token)
    h.call(server, "system.info", token=wrong_token, expect_ok=False)
    h.check("private draft/live separation, stale revision conflicts, read/draft token scope and invalid-token failures")

    mcp.call("posts.unpublish", {"id": post_id, "expected_revision": revision, "confirm": True})
    h.http(server, "/api/public/posts/e2e-lifecycle", 404)
    h.call(server, "posts.delete", {"id": post_id, "expected_revision": revision, "confirm": True})
    require(not h.call(server, "posts.list")["data"]["posts"], "Trash leaked into normal authoring list")
    trash = h.call(server, "posts.list", {"status": "trash"})["data"]["posts"]
    require(len(trash) == 1 and trash[0]["id"] == post_id, "Trashed post is not discoverable")
    recovered = mcp.call("posts.recover", {"id": post_id, "expected_revision": trash[0]["revision"], "confirm": True})["data"]["post"]
    require(recovered["status"] == "draft", "Trash recovery did not create a private draft")
    h.http(server, "/api/public/posts/e2e-lifecycle", 404)
    restored = h.call(server, "posts.restore", {"id": post_id, "revision": post["revision"], "expected_revision": recovered["revision"]})["data"]["post"]
    require(restored["markdown"] == original, "Historical restore did not restore content")
    h.call(server, "posts.publish", {"id": post_id, "expected_revision": restored["revision"], "confirm": True})
    h.check("unpublish, trash listing, private recovery and historical revision restore")

    png64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jDA0AAAAASUVORK5CYII="
    media = mcp.call("media.upload", {"name": "e2e.png", "base64": png64})["data"]["media"]
    require(h.http(server, media["url"]) == base64.b64decode(png64), "Uploaded image bytes did not round-trip")
    require("base64" not in h.call(server, "media.list")["data"]["media"][0], "Media listing exposed base64 content")
    audit = h.call(server, "audit.list")["data"]
    require("E2E private draft" not in json.dumps(audit), "Audit log contained article body")
    h.check("image BLOB round-trip and body-free audit history")
    mcp.close()
    h.stop(server["proc"])
    server = h.start("source", initialize=False)
    require(json.loads(h.http(server, "/api/public/posts/e2e-lifecycle"))["post"]["markdown"] == original, "Publication did not survive daemon restart")
    require(h.http(server, media["url"]) == base64.b64decode(png64), "Media did not survive daemon restart")
    h.check("restart persistence, clean MCP EOF and JSON-RPC-only stdout")
    backup = h.call(server, "backup.export")["data"]
    require(backup["format"] == "folio-backup" and backup["sha256"], "Backup is missing integrity metadata")
    require(backup["state"]["media"][media["id"]]["base64"] == png64, "Backup is missing image bytes")
    target = h.start("restored")
    bad = copy.deepcopy(backup)
    bad["sha256"] = "0" * 64
    h.call(target, "backup.restore", {"backup": bad, "confirm": True}, expect_ok=False, use_stdin=True)
    h.call(target, "backup.restore", {"backup": backup, "confirm": False}, expect_ok=False, use_stdin=True)
    h.call(target, "backup.restore", {"backup": backup, "confirm": True}, use_stdin=True)
    restored_public = json.loads(h.http(target, "/api/public/posts/e2e-lifecycle"))["post"]
    require(restored_public["markdown"] == original, "Restored live publication differs")
    require(h.http(target, media["url"]) == base64.b64decode(png64), "Restored image bytes differ")
    h.call(target, "backup.restore", {"backup": backup, "confirm": True}, expect_ok=False, use_stdin=True)
    require(len(h.call(target, "posts.get", {"id": post_id})["data"]["revisions"]) >= 4, "Backup lost revision history")
    h.check("checksum/confirmation/empty-instance guards and full logical backup restore")
    h.graceful_shutdown()
    h.check_no_secrets()
    h.check("no owner/read/draft/invalid token values in CLI/MCP/public outputs or daemon logs")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="dist/folio", type=Path)
    args = parser.parse_args()
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error("Build the FOLIO binary first: go build -buildvcs=false -o dist/folio ./cmd/folio")
    with tempfile.TemporaryDirectory(prefix="folio-e2e-") as temporary:
        harness = Harness(binary, Path(temporary))
        try:
            lifecycle(harness)
        finally:
            harness.close()
        print("PASS all " + str(len(harness.checks)) + " real-binary lifecycle groups")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("FAIL " + type(error).__name__ + ": " + str(error), file=sys.stderr)
        sys.exit(1)
