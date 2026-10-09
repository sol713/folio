#!/usr/bin/env python3
"""Allowlist-only source packaging: exclude data, tokens, binaries and backups."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parents[1]
names = [".gitignore", ".dockerignore", "LICENSE", "Makefile", "README.md", "REFERENCES.md", "ops.py",
         "deploy/Dockerfile", "scripts/check.py", "scripts/package.py", "tests/test_ops.py",
         "tests/fixtures/folio_fixture.py", "docs/deployment.md", "docs/backup-recovery.md",
         "docs/upgrade-rollback.md", "docs/security.md", "docs/test-results.json",
         "tests/integration.py", "docs/integration.md", "docs/integration-results.json",
         "docs/upstream-manifest.json"]
files = {}
for name in names:
    path = root / name
    if not path.is_file() or path.is_symlink():
        raise SystemExit("missing or linked allowlisted source: " + name)
    raw = path.read_bytes()
    files[name] = {"size": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
manifest = json.dumps({"format": "agent-blog-ops-source", "version": 1, "files": files}, indent=2).encode()
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--manifest-only", action="store_true")
parser.add_argument("--output", type=Path, default=root.parent / "deliverables")
args = parser.parse_args()
(root / "SOURCE-MANIFEST.json").write_bytes(manifest + b"\n")
if args.manifest_only:
    print(json.dumps({"manifest": "SOURCE-MANIFEST.json", "source_files": len(names)}))
    raise SystemExit(0)
output = args.output
output.mkdir(mode=0o700, exist_ok=True)
archive = output / "agent-blog-ops-source.tar.gz"
with tarfile.open(archive, "w:gz") as tar:
    for name in names:
        tar.add(root / name, arcname="agent-blog-ops/" + name, recursive=False)
    entry = tarfile.TarInfo("agent-blog-ops/SOURCE-MANIFEST.json")
    entry.size, entry.mode = len(manifest), 0o644
    tar.addfile(entry, io.BytesIO(manifest))
archive.chmod(0o600)
print(json.dumps({"path": str(archive), "size": archive.stat().st_size,
                  "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "source_files": len(names),
                  "contains_runtime_data": False}))
