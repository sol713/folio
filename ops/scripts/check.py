#!/usr/bin/env python3
"""Run automated checks and persist concise, machine-readable evidence."""
import datetime
import json
from pathlib import Path
import platform
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
result = subprocess.run([sys.executable, "-m", "unittest", "discover", "-s", "tests", "-v"],
                        cwd=root, capture_output=True, text=True)
print(result.stderr, end="")
report = {"at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
          "python": platform.python_version(), "platform": platform.system() + " " + platform.machine(),
          "command": "python3 -m unittest discover -s tests -v", "exit_code": result.returncode,
          "stdout": result.stdout, "stderr": result.stderr,
          "scope": "real SQLite and temporary loopback HTTP; synthetic folio protocol; simulated Docker driver"}
(root / "docs" / "test-results.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
sys.exit(result.returncode)
