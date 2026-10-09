#!/usr/bin/env python3
"""Folio operations, Python >=3.11 standard library only. No downloads or shell eval."""
from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import io
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import socket
import sqlite3
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

MAX_BACKUP = 4 * 1024**3
MAX_LOGICAL = 64 * 1024**2
HEALTH_COMMAND = ["/folio", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]
DIGEST = re.compile(r"(?:sha256:[0-9a-f]{64}|[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64})\Z")


class OpsError(Exception):
    pass


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def safe_rel(name: str) -> str:
    p = PurePosixPath(name)
    if not name or p.is_absolute() or ".." in p.parts or "\\" in name or str(p) != name:
        raise OpsError("unsafe relative path")
    return name


def local_path(value: str | Path) -> Path:
    # Reject symbolic links in every component before resolving the path.
    p = Path(os.path.abspath(value))
    # macOS provides /var and /tmp as fixed OS aliases to /private. Accept only
    # these platform-owned aliases; user-created links are still refused.
    if sys.platform == "darwin" and (str(p) == "/var" or str(p).startswith("/var/")
                                    or str(p) == "/tmp" or str(p).startswith("/tmp/")):
        p = Path("/private" + str(p))
    for part in [p, *p.parents]:
        if part.is_symlink():
            raise OpsError("symbolic links are not accepted")
    return p


def write_private(path: Path, data: bytes, replace: bool = False) -> None:
    path = local_path(path)
    if not path.parent.is_dir():
        raise OpsError("output parent does not exist")
    if path.exists() and not replace:
        raise OpsError("output already exists")
    fd, tmp = tempfile.mkstemp(prefix=".folio-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        if replace:
            os.replace(tmp, path)
        else:
            os.link(tmp, path)  # Atomic no-clobber publication, unlike rename.
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def json_bytes(value) -> bytes:
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


def token_file(path: Path) -> str:
    path = local_path(path)
    mode = path.stat().st_mode
    if not stat.S_ISREG(mode) or stat.S_IMODE(mode) & 0o077:
        raise OpsError("token must be a private regular file (0600)")
    value = path.read_text().strip()
    if len(value) < 32 or "\n" in value or "\r" in value:
        raise OpsError("token is invalid or shorter than 32 characters")
    return value


def data_permissions(root: Path, uid: int) -> None:
    """Check the numeric bind-mount owner, not just the invoking user's access."""
    root = local_path(root)
    if not root.is_dir():
        raise OpsError("initialize the data directory first")
    paths = [(root, 0o700), (root / "token", 0o400), (root / "folio.db", 0o600)]
    paths.extend((root / name, 0o600) for name in ("folio.db-wal", "folio.db-shm")
                 if (root / name).exists())
    for path, required in paths:
        info = local_path(path).stat()
        mode = stat.S_IMODE(info.st_mode)
        if info.st_uid != uid:
            raise OpsError("data, token and database must be owned by the configured runtime UID; initialize as that user")
        # SQLite creates 0644 database/WAL files under the private 0700 root by
        # default. That root is the privacy boundary; reject writable sharing.
        forbidden = 0o077 if path in (root, root / "token") else 0o022
        if mode & forbidden or mode & required != required:
            raise OpsError("data root and token must be private; database files must be owner-writable and not group/other-writable")
        if path != root and not stat.S_ISREG(info.st_mode):
            raise OpsError("data files must be regular files")
    token_file(root / "token")


def safe_url(url: str) -> str:
    p = urllib.parse.urlsplit(url)
    if p.scheme not in ("http", "https") or not p.hostname or p.username or p.password:
        raise OpsError("URL must be HTTP(S), with no embedded credentials")
    if p.path not in ("", "/") or p.query or p.fragment:
        raise OpsError("URL must be an origin without path, query or fragment")
    try:
        port = p.port
        if port is not None and not 1 <= port <= 65535:
            raise ValueError()
    except ValueError:
        raise OpsError("invalid URL port") from None
    if p.scheme == "http":
        try:
            loopback = ipaddress.ip_address(p.hostname).is_loopback
        except ValueError:
            loopback = p.hostname.lower() == "localhost"
        if not loopback:
            raise OpsError("non-loopback HTTP is refused; use HTTPS")
    return url.rstrip("/")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def probe(url: str, ready: bool = False, timeout: float = 3) -> dict:
    target = safe_url(url) + ("/readyz" if ready else "/healthz")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(target, timeout=timeout) as response:
            body = response.read(8193)
            if len(body) > 8192:
                raise OpsError("health response exceeds limit")
            data = json.loads(body)
            if response.status != 200 or data.get("status") != "ok":
                raise OpsError("health endpoint did not report ok")
            return {"status": "ok", "version": str(data.get("version", "unknown"))[:80]}
    except urllib.error.HTTPError as e:
        e.close()
        raise OpsError("health check failed (response body suppressed)") from None
    except (urllib.error.URLError, ValueError, AttributeError, TimeoutError, OSError):
        raise OpsError("health check failed (response body suppressed)") from None


def validate_snapshot(data: dict) -> None:
    if (not isinstance(data, dict) or data.get("format") != "folio-backup"
            or type(data.get("version")) is not int or data["version"] not in (1, 2)
            or not isinstance(data.get("state"), dict)
            or not re.fullmatch(r"[0-9a-f]{64}", str(data.get("sha256", "")))):
        raise OpsError("unsupported logical snapshot")
    schema = data["state"].get("schema")
    if "schema" in data["state"] and (type(schema) is not int or schema != data["version"]):
        raise OpsError("logical backup version and state schema do not match")
    # The application's Go serializer/hash is authoritative; restore validates it.


def folio_call(binary: Path, url: str, token: Path, args: list[str], timeout: int = 60) -> dict:
    binary = local_path(binary)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise OpsError("trusted folio executable is missing or not executable")
    env = os.environ.copy()
    env["FOLIO_URL"] = safe_url(url)
    env["FOLIO_TOKEN"] = token_file(token)
    try:
        result = subprocess.run([str(binary), *args], env=env, capture_output=True, timeout=timeout)
        if result.returncode != 0:
            raise OpsError("folio operation failed; stderr suppressed to protect credentials")
        data = json.loads(result.stdout)
        if not isinstance(data, dict) or data.get("ok") is not True:
            raise OpsError("folio operation returned failure")
        return data
    except (subprocess.TimeoutExpired, ValueError, OSError):
        raise OpsError("folio invocation failed; command output suppressed") from None


def logical_export(binary: Path, url: str, token: Path, output: Path) -> dict:
    data = folio_call(binary, url, token, ["call", "backup.export", "--json", "{}"])["data"]
    validate_snapshot(data)
    raw = json_bytes(data)
    if len(raw) > MAX_LOGICAL:
        raise OpsError("logical snapshot exceeds the 64 MiB limit")
    write_private(output, raw)
    return {"format": data["format"], "version": data["version"], "schema": data["state"].get("schema"),
            "output": str(output), "file_sha256": sha256(output),
            "state_sha256": data["sha256"], "contains_token": False}


def logical_restore(binary: Path, url: str, token: Path, backup: Path) -> dict:
    backup = local_path(backup)
    if backup.stat().st_size > MAX_LOGICAL:
        raise OpsError("snapshot exceeds size limit")
    data = json.loads(backup.read_bytes())
    validate_snapshot(data)
    with tempfile.TemporaryDirectory(prefix="folio-restore-") as work:
        request = Path(work) / "request.json"
        write_private(request, json_bytes({"backup": data, "confirm": True}))
        restored = folio_call(binary, url, token, ["call", "backup.restore", "--file", str(request)])["data"]
    source_schema = data["state"].get("schema")
    target_schema = restored.get("schema")
    if target_schema is None:
        # Older daemons do not report schema metadata in the restore response.
        # Observe the actual target without guessing from localized help/version text.
        observed = folio_call(binary, url, token, ["call", "backup.export", "--json", "{}"])["data"]
        validate_snapshot(observed)
        target_schema = observed["state"].get("schema")
    return {"status": "restored", "token_preserved": True,
            "source_schema": source_schema, "schema": target_schema,
            "schema_upgraded": source_schema == 1 and target_schema == 2}


def tree(root: Path) -> tuple[list[str], list[Path]]:
    root = local_path(root)
    if not root.is_dir():
        raise OpsError("data directory does not exist")
    dirs, files = [], []
    for current, subdirs, names in os.walk(root, followlinks=False):
        for name in sorted(subdirs + names):
            p = Path(current) / name
            mode = p.lstat().st_mode
            if stat.S_ISLNK(mode) or not (stat.S_ISDIR(mode) or stat.S_ISREG(mode)):
                raise OpsError("data tree contains a link or special file")
            rel = safe_rel(p.relative_to(root).as_posix())
            if stat.S_ISDIR(mode):
                dirs.append(rel)
            else:
                files.append(p)
    return sorted(dirs), sorted(files)


def sqlite_check(root: Path) -> None:
    db = local_path(root / "folio.db")
    if not db.is_file() or db.stat().st_size == 0:
        raise OpsError("folio.db missing or empty")
    try:
        with contextlib.closing(sqlite3.connect(db.as_uri() + "?mode=ro", uri=True)) as conn:
            if conn.execute("PRAGMA quick_check").fetchall() != [("ok",)]:
                raise OpsError("SQLite quick_check failed")
    except sqlite3.Error:
        raise OpsError("SQLite quick_check failed") from None


def offline_backup(root: Path, output: Path, stopped: bool = False) -> dict:
    if not stopped:
        raise OpsError("offline backup requires a proven stopped service")
    root, output = local_path(root), local_path(output)
    if output.is_relative_to(root):
        raise OpsError("backup output must be outside data directory")
    dirs, files = tree(root)
    token_file(root / "token")
    with tempfile.TemporaryDirectory(prefix="folio-backup-") as work:
        stage = local_path(work) / "payload"
        stage.mkdir(mode=0o700)
        for rel in dirs:
            (stage / rel).mkdir(mode=0o700)
        for p in files:
            target = stage / p.relative_to(root)
            shutil.copyfile(p, target, follow_symlinks=False)
            target.chmod(0o600)
        sqlite_check(stage)
        dirs, files = tree(stage)
        entries = {p.relative_to(stage).as_posix(): {"sha256": sha256(p), "size": p.stat().st_size}
                   for p in files}
        manifest = {"format": "folio-offline", "version": 1, "contains_token": True,
                    "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                    "directories": dirs, "files": entries}
        archive = local_path(work) / "backup.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            raw = json_bytes(manifest)
            info = tarfile.TarInfo("manifest.json")
            info.size, info.mode = len(raw), 0o600
            tar.addfile(info, io.BytesIO(raw))
            for rel in dirs:
                info = tarfile.TarInfo("payload/" + rel)
                info.type, info.mode = tarfile.DIRTYPE, 0o700
                tar.addfile(info)
            for p in files:
                tar.add(p, arcname="payload/" + p.relative_to(stage).as_posix(), recursive=False)
        # Avoid reading large archives into RAM; publish the already-private temporary file.
        fd, tmp = tempfile.mkstemp(prefix=".folio-", dir=output.parent)
        os.close(fd)
        try:
            shutil.copyfile(archive, tmp)
            with open(tmp, "rb") as f:
                os.fsync(f.fileno())
            os.link(tmp, output)
        finally:
            os.unlink(tmp)
    return {"output": str(output), "sha256": sha256(output), "files": len(entries),
            "contains_token": True}


def unpack_verified(backup: Path, stage: Path) -> dict:
    stage = local_path(stage)
    seen, total, manifest = set(), 0, None
    with tarfile.open(local_path(backup), "r:gz") as tar:
        for member in tar:
            name = safe_rel(member.name)
            if name in seen or not (member.isfile() or member.isdir()):
                raise OpsError("archive has duplicate, link or special entry")
            seen.add(name)
            total += member.size
            if total > MAX_BACKUP or member.size < 0:
                raise OpsError("archive exceeds size limit")
            if name == "manifest.json" and member.isfile():
                if member.size > 16 * 1024**2:
                    raise OpsError("manifest exceeds size limit")
                manifest = json.load(tar.extractfile(member))
                continue
            if not name.startswith("payload/"):
                raise OpsError("unexpected archive entry")
            target = stage / safe_rel(name[8:])
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True, mode=0o700)
            else:
                target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with target.open("xb") as f, tar.extractfile(member) as source:
                    shutil.copyfileobj(source, f)
                target.chmod(0o600)
    if not isinstance(manifest, dict) or manifest.get("format") != "folio-offline" or manifest.get("version") != 1:
        raise OpsError("unsupported offline manifest")
    dirs, files = tree(stage)
    actual = {p.relative_to(stage).as_posix(): {"sha256": sha256(p), "size": p.stat().st_size}
              for p in files}
    if actual != manifest.get("files") or dirs != manifest.get("directories"):
        raise OpsError("backup integrity verification failed")
    token_file(stage / "token")
    sqlite_check(stage)
    return manifest


def offline_restore(backup: Path, target: Path) -> dict:
    target = local_path(target)
    if target.exists():
        raise OpsError("restore target must not exist")
    with tempfile.TemporaryDirectory(prefix=".folio-restore-", dir=target.parent) as work:
        stage = Path(work) / "payload"
        stage.mkdir(mode=0o700)
        manifest = unpack_verified(backup, stage)
        # Exclusive directory creation prevents overwriting a concurrent target.
        target.mkdir(mode=0o700)
        try:
            shutil.copytree(stage, target, dirs_exist_ok=True)
        except BaseException:
            shutil.rmtree(target)
            raise
    return {"target": str(target), "files": len(manifest["files"]), "sqlite": "ok",
            "contains_token": True}


def verify_offline(backup: Path) -> dict:
    with tempfile.TemporaryDirectory(prefix="folio-verify-") as work:
        manifest = unpack_verified(backup, Path(work))
    return {"status": "verified", "files": len(manifest["files"]), "sqlite": "ok",
            "contains_token": True}


def static_elf(binary: Path, arch: str) -> None:
    with binary.open("rb") as f:
        header = f.read(64)
        if len(header) != 64 or header[:6] != b"\x7fELF\x02\x01":
            raise OpsError("expected little-endian 64-bit Linux ELF")
        machine = struct.unpack_from("<H", header, 18)[0]
        if machine != {"amd64": 62, "arm64": 183}[arch]:
            raise OpsError("ELF architecture does not match target")
        offset = struct.unpack_from("<Q", header, 32)[0]
        size, count = struct.unpack_from("<HH", header, 54)
        if size < 56 or not count or offset + size * count > binary.stat().st_size:
            raise OpsError("malformed ELF program headers")
        for i in range(count):
            f.seek(offset + i * size)
            if struct.unpack("<I", f.read(4))[0] == 3:  # PT_INTERP
                raise OpsError("dynamic ELF cannot run in scratch; build CGO_ENABLED=0")


def prepare_image(binary: Path, digest: str, ca: Path, ca_digest: str, out: Path, arch: str) -> dict:
    binary, ca, out = local_path(binary), local_path(ca), local_path(out)
    if not re.fullmatch(r"[0-9a-f]{64}", digest) or sha256(binary) != digest:
        raise OpsError("binary SHA256 does not match trusted release")
    if not re.fullmatch(r"[0-9a-f]{64}", ca_digest) or sha256(ca) != ca_digest:
        raise OpsError("CA bundle SHA256 does not match supplied value")
    static_elf(binary, arch)
    if b"-----BEGIN CERTIFICATE-----" not in ca.read_bytes():
        raise OpsError("CA bundle is not PEM")
    out.mkdir(mode=0o700)  # Never overwrite an earlier artifact set.
    try:
        shutil.copyfile(binary, out / "folio")
        (out / "folio").chmod(0o555)
        shutil.copyfile(ca, out / "ca-certificates.crt")
        (out / "ca-certificates.crt").chmod(0o444)
        write_private(out / "provenance.json", json_bytes({"binary_sha256": digest,
                       "ca_sha256": ca_digest, "arch": arch, "static": True}))
    except BaseException:
        shutil.rmtree(out)
        raise
    return {"output": str(out), "arch": arch, "binary_sha256": digest}


def compose_document(root: Path, image: str, port: int, uid: int, gid: int, health: list[str] | None = None) -> dict:
    root = local_path(root)
    if not DIGEST.fullmatch(image):
        raise OpsError("image must be an immutable sha256 ID or repository digest")
    if not 1024 <= port <= 65535 or uid <= 0 or gid < 0:
        raise OpsError("use an unprivileged host port and non-root UID")
    data_permissions(root, uid)
    if "$" in str(root):
        raise OpsError("Compose interpolation characters in data path are refused")
    service = {"image": image, "pull_policy": "never", "user": f"{uid}:{gid}",
               "entrypoint": ["/folio"], "command": ["serve", "--data", "/data", "--addr", "0.0.0.0:8080"],
               "ports": [f"127.0.0.1:{port}:8080"], "environment": {"FOLIO_URL": "http://127.0.0.1:8080"},
               "volumes": [{"type": "bind", "source": str(root), "target": "/data",
                            "bind": {"create_host_path": False}}],
               "read_only": True, "cap_drop": ["ALL"], "security_opt": ["no-new-privileges:true"],
               "tmpfs": ["/tmp:rw,noexec,nosuid,size=16m,mode=1777"],
               "pids_limit": 128, "mem_limit": "512m", "cpus": 1.0,
               "restart": "unless-stopped", "stop_grace_period": "15s", "stop_signal": "SIGTERM",
               "logging": {"driver": "json-file", "options": {"max-size": "10m", "max-file": "3"}}}
    health = HEALTH_COMMAND if health is None else health
    if health:
        if not isinstance(health, list) or not all(isinstance(x, str) and x for x in health) or health[0] != "/folio":
            raise OpsError("health command must be a /folio argument array")
        service["healthcheck"] = {"test": ["CMD", *health], "interval": "30s", "timeout": "5s",
                                  "start_period": "10s", "retries": 3}
    return {"name": "folio", "services": {"blog": service}}


def run(argv: list[str], timeout: int = 60) -> str:
    try:
        result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        if result.returncode:
            raise OpsError("external command failed (output suppressed)")
        return result.stdout
    except (OSError, subprocess.TimeoutExpired):
        raise OpsError("external command unavailable or timed out") from None


class Engine:
    def __init__(self):
        if shutil.which("docker"):
            try:
                run(["docker", "compose", "version"])
                self.compose = ["docker", "compose"]
            except OpsError:
                if not shutil.which("docker-compose"):
                    raise OpsError("Compose v2+ or docker-compose is required")
                self.compose = ["docker-compose"]
        else:
            raise OpsError("Docker is not installed")
        run(["docker", "info"], timeout=10)

    def cmd(self, config: Path, *args) -> str:
        return run([*self.compose, "--project-name", "folio", "-f", str(config), *args])

    def present(self, image: str):
        run(["docker", "image", "inspect", image])  # No network pulls.

    def stop(self, config: Path):
        self.cmd(config, "stop", "--timeout", "15", "blog")
        ids = self.cmd(config, "ps", "--all", "--quiet", "blog").split()
        for container in ids:
            state = run(["docker", "inspect", "--format", "{{.State.Running}}", container]).strip()
            if state != "false":
                raise OpsError("container is still running; backup refused")

    def start(self, config: Path):
        self.cmd(config, "up", "-d", "--no-build", "--pull", "never", "blog")

    def wait(self, url: str, timeout: int = 30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                probe(url, ready=True, timeout=2)
                return
            except OpsError:
                time.sleep(0.5)
        raise OpsError("deployment did not become healthy")


def deployment(config: Path) -> tuple[dict, Path, str]:
    document = json.loads(local_path(config).read_bytes())
    try:
        service = document["services"]["blog"]
        image = service["image"]
        root = local_path(service["volumes"][0]["source"])
        port = int(service["ports"][0].split(":")[1])
        uid, gid = map(int, service["user"].split(":"))
        expected = compose_document(root, image, port, uid, gid,
                    service.get("healthcheck", {}).get("test", [])[1:])
        if document != expected:
            raise OpsError("deployment config differs from generated security contract")
        return document, root, f"http://127.0.0.1:{port}"
    except (KeyError, IndexError, ValueError, TypeError):
        raise OpsError("invalid generated Compose JSON") from None


@contextlib.contextmanager
def deployment_lock(config: Path):
    lock = local_path(config.with_suffix(config.suffix + ".lock"))
    fd = os.open(lock, os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise OpsError("another deployment operation is running") from None
        yield
    finally:
        os.close(fd)


def rollback_data(engine, current: Path, old: Path, root: Path, backup: Path, url: str) -> Path:
    engine.stop(current)  # Never restore underneath a running process.
    failed = root.with_name(root.name + ".failed-" + uuid.uuid4().hex)
    root.rename(failed)  # Keep post-upgrade data for inspection, never delete it.
    offline_restore(backup, root)
    engine.start(old)
    engine.wait(url)
    return failed


def upgrade(config: Path, image: str, backup_dir: Path, engine=None) -> dict:
    config, backup_dir = local_path(config), local_path(backup_dir)
    if not DIGEST.fullmatch(image):
        raise OpsError("upgrade requires an immutable image digest")
    with deployment_lock(config):
        document, root, url = deployment(config)
        if root.stat().st_uid != os.getuid() or int(document["services"]["blog"]["user"].split(":")[0]) != os.getuid():
            raise OpsError("run deployment operations as the configured data owner")
        journal_path = config.with_suffix(".upgrade.json")
        if journal_path.exists():
            previous = json.loads(journal_path.read_bytes())
            if previous.get("status") not in ("deployed", "rolled_back"):
                raise OpsError("incomplete upgrade journal; inspect before proceeding")
        if image == document["services"]["blog"]["image"]:
            raise OpsError("new image matches current image")
        if backup_dir.is_relative_to(root):
            raise OpsError("backup directory must be outside data root")
        engine = engine or Engine()
        engine.present(image)
        engine.present(document["services"]["blog"]["image"])
        engine.wait(url)
        backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        suffix = uuid.uuid4().hex
        old = config.with_name(config.stem + ".previous-" + suffix + ".json")
        candidate = config.with_name(config.stem + ".candidate-" + suffix + ".json")
        backup = backup_dir / ("pre-upgrade-" + suffix + ".tar.gz")
        write_private(old, config.read_bytes())
        new_document = json.loads(json.dumps(document))
        new_document["services"]["blog"]["image"] = image
        write_private(candidate, json_bytes(new_document))
        journal = {"status": "stopping", "config": str(config), "old_config": str(old),
                   "candidate": str(candidate), "backup": str(backup), "root": str(root),
                   "url": url, "old_image": document["services"]["blog"]["image"], "new_image": image}
        write_private(journal_path, json_bytes(journal), replace=journal_path.exists())
        engine.stop(config)
        try:
            offline_backup(root, backup, stopped=True)
        except BaseException:
            engine.start(old)
            journal["status"] = "backup_failed"
            write_private(journal_path, json_bytes(journal), replace=True)
            raise
        journal["backup_sha256"], journal["status"] = sha256(backup), "launching"
        write_private(journal_path, json_bytes(journal), replace=True)
        try:
            engine.start(candidate)
            engine.wait(url)
        except OpsError:
            journal["status"] = "rollback_pending"
            write_private(journal_path, json_bytes(journal), replace=True)
            failed = rollback_data(engine, candidate, old, root, backup, url)
            journal.update(status="rolled_back", failed_data=str(failed))
            write_private(journal_path, json_bytes(journal), replace=True)
            raise OpsError("upgrade failed; previous image and pre-upgrade data restored") from None
        write_private(config, json_bytes(new_document), replace=True)
        journal["status"] = "deployed"
        write_private(journal_path, json_bytes(journal), replace=True)
        return {"status": "deployed", "image": image, "backup": str(backup), "journal": str(journal_path)}


def rollback(config: Path, engine=None) -> dict:
    config = local_path(config)
    with deployment_lock(config):
        document, root, url = deployment(config)
        if root.stat().st_uid != os.getuid() or int(document["services"]["blog"]["user"].split(":")[0]) != os.getuid():
            raise OpsError("run deployment operations as the configured data owner")
        journal_path = config.with_suffix(".upgrade.json")
        journal = json.loads(local_path(journal_path).read_bytes())
        if (journal.get("status") != "deployed" or journal.get("root") != str(root)
                or journal.get("config") != str(config) or journal.get("url") != url
                or journal.get("new_image") != document["services"]["blog"]["image"]):
            raise OpsError("journal does not match current deployment")
        backup, old = local_path(journal["backup"]), local_path(journal["old_config"])
        old_document, old_root, old_url = deployment(old)
        if (old_root != root or old_url != url or old_document["services"]["blog"]["image"] != journal["old_image"]
                or sha256(backup) != journal["backup_sha256"]):
            raise OpsError("rollback configuration or backup was altered")
        verify_offline(backup)
        engine = engine or Engine()
        engine.present(journal["old_image"])
        journal["status"] = "rollback_pending"
        write_private(journal_path, json_bytes(journal), replace=True)
        failed = rollback_data(engine, config, old, root, backup, url)
        write_private(config, old.read_bytes(), replace=True)
        journal.update(status="rolled_back", failed_data=str(failed))
        write_private(journal_path, json_bytes(journal), replace=True)
        return {"status": "rolled_back", "preserved_data": str(failed)}


def doctor(data: Path | None = None) -> dict:
    checks = [{"check": "python", "ok": sys.version_info >= (3, 11), "version": sys.version.split()[0]},
              {"check": "sqlite", "ok": True, "version": sqlite3.sqlite_version}]
    for name, args in [("go", ["go", "version"]), ("docker", ["docker", "version"]),
                       ("compose", ["docker", "compose", "version"]), ("git", ["git", "--version"])]:
        try:
            if name == "compose":
                try:
                    version = run(args, timeout=10)
                except OpsError:
                    version = run(["docker-compose", "version"], timeout=10)
            else:
                version = run(args, timeout=10)
            checks.append({"check": name, "ok": True, "version": version.strip()[:120]})
        except OpsError:
            checks.append({"check": name, "ok": False, "detail": "unavailable or not ready"})
    if data:
        try:
            data_permissions(data, os.getuid())
            tree(data)
            checks.append({"check": "data_permissions", "ok": True})
        except (OpsError, OSError):
            checks.append({"check": "data_permissions", "ok": False})
    return {"checks": checks, "container_runtime_ready": any(c["check"] == "docker" and c["ok"] for c in checks)}


def verify_restored_state(source: dict, restored: dict) -> dict:
    """Folio restores content exactly, then records one audited state transition.

    The Go application validates each backup checksum. We deliberately do not
    reproduce its JSON canonicalization or compare whole-state hashes: restore
    itself changes the instance revision and appends an audit event.
    """
    before, after = source.get("state"), restored.get("state")
    if not isinstance(before, dict) or not isinstance(after, dict):
        raise OpsError("logical state must be an object")
    preserved_before = {k: v for k, v in before.items() if k not in ("revision", "audit")}
    preserved_after = {k: v for k, v in after.items() if k not in ("revision", "audit")}
    source_schema, target_schema = before.get("schema"), after.get("schema")
    schema_upgraded = source_schema == 1 and target_schema == 2
    if schema_upgraded:
        if source.get("version") != 1 or restored.get("version") != 2:
            raise OpsError("schema upgrade requires matching v1 and v2 backup envelopes")
        preserved_before["schema"] = 2
        # v1 never contained workflows. Only newly empty maps may be introduced;
        # populated proposal/schedule data is never normalized away.
        for field in ("proposals", "schedules"):
            if before.get(field, {}) != {} or after.get(field, {}) != {}:
                raise OpsError("legacy schema migration must not introduce or discard workflow content")
            preserved_before.pop(field, None)
            preserved_after.pop(field, None)
    elif source_schema != target_schema:
        raise OpsError("unsupported restore schema transition")
    if preserved_before != preserved_after:
        raise OpsError("restored content, settings or media differs from source")
    revision = before.get("revision")
    if type(revision) is not int or after.get("revision") != revision + 1:
        raise OpsError("restore did not advance the instance revision exactly once")
    old_audit, new_audit = before.get("audit"), after.get("audit")
    if not isinstance(old_audit, list) or not isinstance(new_audit, list) or not new_audit:
        raise OpsError("restore audit trail is missing")
    event = new_audit[-1]
    if (not isinstance(event, dict) or event.get("operation") != "backup.restore"
            or event.get("target") != "instance" or event.get("revision") != revision
            or set(event) != {"id", "operation", "target", "actor", "at", "revision"}
            or event.get("actor") != "admin"
            or not isinstance(event.get("id"), str)
            or not re.fullmatch(r"[0-9a-f]{24}", event["id"])
            or any(item.get("id") == event["id"] for item in old_audit)
            or not isinstance(event.get("at"), str)
            or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z", event["at"])
            or new_audit != (old_audit + [event])[-10000:]):
        raise OpsError("unexpected restore audit transition")
    try:
        dt.datetime.fromisoformat(event["at"])
    except ValueError:
        raise OpsError("invalid restore audit timestamp") from None
    return {"source_schema": source_schema, "schema": target_schema,
            "schema_upgraded": schema_upgraded}


def supports_paused_schedules(binary: Path, env: dict | None = None) -> bool:
    """Machine version discovery keeps legacy v0.1 recovery usable."""
    try:
        result = subprocess.run([str(binary), "version"], env=env, capture_output=True, timeout=10)
        data = json.loads(result.stdout)
        match = re.fullmatch(r"(\d+)\.(\d+)\.\d+(?:[-+].*)?", data.get("version", ""))
        if result.returncode or not match:
            raise OpsError("cannot determine trusted binary scheduler support")
        return (int(match[1]), int(match[2])) >= (0, 2)
    except (OSError, ValueError, TypeError, AttributeError, subprocess.TimeoutExpired):
        raise OpsError("cannot determine trusted binary scheduler support") from None


def logical_drill(binary: Path, url: str, token: Path, workdir: Path) -> dict:
    """Restore into an isolated instance, verify content and audit, then stop it."""
    binary, workdir = local_path(binary), local_path(workdir)
    workdir.mkdir(mode=0o700)  # Refuse existing directories, including prior drills.
    backup = workdir / "logical.json"
    exported = logical_export(binary, url, token, backup)
    data = workdir / "data"
    env = os.environ.copy()
    for key in ("FOLIO_TOKEN", "FOLIO_DRAFT_TOKEN", "FOLIO_READ_TOKEN"):
        env.pop(key, None)
    pause_schedules = supports_paused_schedules(binary, env)
    try:
        initialized = subprocess.run([str(binary), "init", "--data", str(data)], env=env,
                                     capture_output=True, timeout=30)
        if initialized.returncode:
            raise OpsError("isolated instance init failed; output suppressed")
    except (OSError, subprocess.TimeoutExpired):
        raise OpsError("isolated instance init failed; output suppressed") from None
    new_token = data / "token"
    if token_file(new_token) == token_file(token):
        raise OpsError("drill token must be independent")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    new_url = f"http://127.0.0.1:{port}"
    with tempfile.TemporaryFile() as stderr:
        command = [str(binary), "serve", "--data", str(data), "--addr", f"127.0.0.1:{port}"]
        if pause_schedules:
            command.append("--pause-schedules")
        proc = subprocess.Popen(command,
                                env=env, stdout=subprocess.DEVNULL, stderr=stderr)
        try:
            deadline = time.monotonic() + 10
            while True:
                if proc.poll() is not None:
                    raise OpsError("isolated server exited; output suppressed")
                try:
                    probe(new_url, ready=True)
                    break
                except OpsError:
                    if time.monotonic() >= deadline:
                        raise OpsError("isolated server did not become healthy") from None
                    time.sleep(0.1)
            if pause_schedules:
                info = folio_call(binary, new_url, new_token, ["call", "system.info", "--json", "{}"])["data"]
                if info.get("scheduler", {}).get("paused") is not True:
                    raise OpsError("isolated recovery scheduler is not paused")
            logical_restore(binary, new_url, new_token, backup)
            result = logical_export(binary, new_url, new_token, workdir / "roundtrip.json")
            transition = verify_restored_state(json.loads(backup.read_bytes()),
                                               json.loads((workdir / "roundtrip.json").read_bytes()))
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
                raise OpsError("isolated server required forced shutdown") from None
    return {"status": "drill_passed", "workdir": str(workdir), "source_unchanged": True,
            **transition,
            "independent_token": True, "content_preserved": True,
            "scheduler_paused": pause_schedules,
            "restore_audit_verified": True, "source_state_sha256": exported["state_sha256"],
            "restored_state_sha256": result["state_sha256"]}


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    commands = p.add_subparsers(dest="command", required=True)
    doctor_p = commands.add_parser("doctor")
    doctor_p.add_argument("--data", type=Path)
    probe_p = commands.add_parser("probe")
    probe_p.add_argument("--url", default="http://127.0.0.1:8080")
    probe_p.add_argument("--ready", action="store_true")
    render = commands.add_parser("render")
    render.add_argument("--data", type=Path, required=True)
    render.add_argument("--image", required=True)
    render.add_argument("--output", type=Path, required=True)
    render.add_argument("--port", type=int, default=8080)
    render.add_argument("--uid", type=int, default=os.getuid())
    render.add_argument("--gid", type=int, default=os.getgid())
    render.add_argument("--health-command", help="confirmed folio command as JSON array")
    prepare = commands.add_parser("prepare-image")
    for arg in ("binary", "ca-bundle", "output"):
        prepare.add_argument("--" + arg, type=Path, required=True)
    prepare.add_argument("--sha256", required=True)
    prepare.add_argument("--ca-sha256", required=True)
    prepare.add_argument("--arch", choices=["amd64", "arm64"], required=True)
    for name in ("logical-export", "logical-restore", "logical-drill"):
        sub = commands.add_parser(name)
        sub.add_argument("--binary", type=Path, required=True)
        sub.add_argument("--url", default="http://127.0.0.1:8080")
        sub.add_argument("--token-file", type=Path, required=True)
        sub.add_argument({"logical-export": "--output", "logical-restore": "--backup", "logical-drill": "--workdir"}[name], type=Path, required=True)
    back = commands.add_parser("offline-backup")
    back.add_argument("--data", type=Path, required=True)
    back.add_argument("--output", type=Path, required=True)
    proof = back.add_mutually_exclusive_group(required=True)
    proof.add_argument("--compose", type=Path, help="stop and inspect generated Compose service first")
    proof.add_argument("--stopped", action="store_true", help="operator attests all writers are stopped")
    restore = commands.add_parser("offline-restore")
    restore.add_argument("--backup", type=Path, required=True)
    restore.add_argument("--target", type=Path, required=True)
    verify = commands.add_parser("verify-offline")
    verify.add_argument("--backup", type=Path, required=True)
    up = commands.add_parser("upgrade")
    up.add_argument("--compose", type=Path, required=True)
    up.add_argument("--image", required=True)
    up.add_argument("--backup-dir", type=Path, required=True)
    down = commands.add_parser("rollback")
    down.add_argument("--compose", type=Path, required=True)
    return p


def main():
    args = parser().parse_args()
    try:
        if args.command == "doctor":
            result = doctor(args.data)
        elif args.command == "probe":
            result = probe(args.url, args.ready)
        elif args.command == "render":
            health = json.loads(args.health_command) if args.health_command else None
            doc = compose_document(args.data, args.image, args.port, args.uid, args.gid, health)
            write_private(args.output, json_bytes(doc))
            result = {"output": str(args.output), "container_healthcheck": "healthcheck" in doc["services"]["blog"]}
        elif args.command == "prepare-image":
            result = prepare_image(args.binary, args.sha256, args.ca_bundle, args.ca_sha256, args.output, args.arch)
        elif args.command == "logical-export":
            result = logical_export(args.binary, args.url, args.token_file, args.output)
        elif args.command == "logical-restore":
            result = logical_restore(args.binary, args.url, args.token_file, args.backup)
        elif args.command == "logical-drill":
            result = logical_drill(args.binary, args.url, args.token_file, args.workdir)
        elif args.command == "offline-backup":
            if args.compose:
                with deployment_lock(args.compose):
                    _, root, _ = deployment(args.compose)
                    if root != local_path(args.data):
                        raise OpsError("data root does not match Compose mount")
                    engine = Engine()
                    engine.stop(args.compose)
                    try:
                        result = offline_backup(root, args.output, stopped=True)
                    finally:
                        engine.start(args.compose)
            else:
                result = offline_backup(args.data, args.output, args.stopped)
        elif args.command == "offline-restore":
            result = offline_restore(args.backup, args.target)
        elif args.command == "verify-offline":
            result = verify_offline(args.backup)
        elif args.command == "upgrade":
            result = upgrade(args.compose, args.image, args.backup_dir)
        elif args.command == "rollback":
            result = rollback(args.compose)
        print(json.dumps(result, ensure_ascii=False))
        return 0
    except (OpsError, OSError, ValueError, KeyError, TypeError, tarfile.TarError) as e:
        # Only OpsError messages are authored safe diagnostics; raw exceptions may contain tokens.
        message = str(e) if isinstance(e, OpsError) else "operation failed; check paths, permissions and file format"
        print(json.dumps({"ok": False, "error": message}, ensure_ascii=False), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
