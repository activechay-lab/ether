#!/usr/bin/env python3
"""Read-only preflight for a FUTURE Ether baseline; writes JSON to stdout.

Rejects missing, dirty, or unexpected Go VCS metadata and verifies the named
container executes the inspected read-only bind-mounted Ether binary.
Does not start Docker containers, build binaries, or alter the live queue.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess


def capture(*args):
    return subprocess.check_output(args, text=True, timeout=30)


def binary_record(go, path, expected):
    if not re.fullmatch(r"[0-9a-f]{40}", expected):
        raise ValueError("expected source revision must be a full lowercase Git SHA")
    path = path.resolve(strict=True)
    before = path.stat()
    info = capture(go, "version", "-m", str(path))
    settings = {}
    toolchain = ""
    for line in info.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) == 2 and parts[0] == "build" and "=" in parts[1]:
            key, value = parts[1].split("=", 1)
            settings[key] = value
        if line.startswith(str(path) + ": "):
            toolchain = line[len(str(path)) + 2:]
    if settings.get("vcs") != "git" or settings.get("vcs.revision") != expected:
        raise ValueError(f"{path.name}: missing or unexpected VCS revision")
    if settings.get("vcs.modified") != "false":
        raise ValueError(f"{path.name}: dirty or missing vcs.modified; cannot certify source")
    with path.open("rb") as binary:
        digest = hashlib.file_digest(binary, "sha256").hexdigest()
    after = path.stat()
    if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
        raise ValueError(f"{path.name}: binary changed during inspection")
    return {"path": str(path), "sha256": digest, "go": toolchain, "build": settings}


def preflight(args):
    server = binary_record(args.go, args.server_binary, args.server_sha)
    harness = binary_record(args.go, args.harness_binary, args.harness_sha)
    container = json.loads(capture("docker", "inspect", args.container))[0]
    executable = "/usr/local/bin/ether"
    mounts = [m for m in container["Mounts"] if m["Destination"] == executable]
    if len(mounts) != 1 or mounts[0]["Type"] != "bind" or mounts[0]["RW"]:
        raise ValueError("container must bind-mount the server binary read-only at /usr/local/bin/ether")
    if Path(mounts[0]["Source"]).resolve() != Path(server["path"]):
        raise ValueError("container mount does not match inspected server binary")
    if container["Config"].get("Entrypoint") or container["Config"]["Cmd"][0] != executable:
        raise ValueError("container must execute the inspected server directly")
    return {"schema": 1, "checked_at": datetime.now(timezone.utc).isoformat(),
            "scope": "preflight only; retain post-run hashes and raw results before acceptance",
            "server": server, "harness": harness,
            "container": {"id": container["Id"], "name": container["Name"],
                          "image_id": container["Image"], "image": container["Config"]["Image"]}}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go")
    parser.add_argument("--server-binary", type=Path, required=True)
    parser.add_argument("--server-sha", required=True)
    parser.add_argument("--harness-binary", type=Path, required=True)
    parser.add_argument("--harness-sha", required=True)
    parser.add_argument("--container", required=True)
    args = parser.parse_args()
    try:
        result = preflight(args)
    except (ValueError, OSError, KeyError, IndexError, subprocess.SubprocessError) as exc:
        parser.exit(1, f"provenance rejected: {exc}\n")
    print(json.dumps(result, indent=2))
