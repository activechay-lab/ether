"""Retain gate work attribution without request credentials or database files."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys


def main():
    root = Path(sys.argv[1])
    output = root / "perf-gate-artifacts"
    output.mkdir(exist_ok=True)
    # Explicit provenance allowlist; never dump the environment or config.
    provenance = {
        key: os.environ.get(key, "")
        for key in ("GITHUB_SHA", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "RUNNER_OS", "RUNNER_ARCH")
    }
    provenance["checkout_sha"] = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], text=True
    ).strip()
    report = root / "perf-gate.md"
    provenance["report_present"] = report.is_file()
    if report.is_file():
        shutil.copyfile(report, output / "report.md")
    # The gate tags contain phase/client/step/sequence, never credentials.
    # Paths are deliberately absent: Stremio puts a grant in the URL path.
    fields = ("tag", "surface", "method", "status", "bytes", "start", "duration",
              "queries", "writes", "rows", "dbTime", "lockWait", "allocBytes", "allocs")
    # QueryStats stores SQL templates, not bound parameters. Also redact SQL
    # literals and comments; keep table names, call sites and all work counters.
    literals = re.compile(r"'(?:''|[^'])*'|\"(?:\"\"|[^\"])*\"|--[^\n]*|/\*.*?\*/", re.S)
    complete = report.is_file()
    for scale in ("small", "large"):
        source = root / "perf-gate-private" / (scale + "-trace.jsonl")
        status = {"records": 0, "malformed_lines": 0, "present": source.is_file()}
        if source.is_file():
            target = output / source.name
            with source.open() as src, target.open("w") as dst:
                for line in src:
                    try:
                        record = json.loads(line)
                        safe = {key: record[key] for key in fields if key in record}
                        safe["statements"] = [
                            {"sql": literals.sub("?", st["sql"]),
                             **{key: st[key] for key in ("site", "count", "rows", "time") if key in st}}
                            for st in record.get("statements", [])
                        ]
                    except (ValueError, TypeError, KeyError, AttributeError):
                        # A timeout may leave a partial last record. Preserve
                        # valid records, record the loss and fail preparation.
                        status["malformed_lines"] += 1
                        continue
                    dst.write(json.dumps(safe) + "\n")
                    status["records"] += 1
            status["sanitized_sha256"] = hashlib.sha256(target.read_bytes()).hexdigest()
        complete = complete and status["records"] > 0 and status["malformed_lines"] == 0
        provenance[scale] = status
    provenance["evidence_complete"] = complete
    (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    return 0 if complete else 1


if __name__ == "__main__":
    sys.exit(main())
