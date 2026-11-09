"""Focused provenance enforcement checks; no Go or Docker command is executed.

Run: PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
    -s scripts/pms-baseline -p test_provenance.py -v
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import provenance


class ProvenanceEnforcementTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="provenance-test-")
        self.addCleanup(directory.cleanup)
        root = Path(directory.name).resolve()
        self.args = argparse.Namespace(
            go="fake-go", server_binary=root / "ether",
            server_sha="a" * 40, harness_binary=root / "etherperf",
            harness_sha="b" * 40, container="fake-ether-container",
        )
        self.metadata = {}
        for role in ("server", "harness"):
            path = getattr(self.args, role + "_binary")
            path.write_bytes(b"non-executable test fixture: " + role.encode())
            self.metadata[str(path)] = {
                "vcs": "git", "vcs.revision": getattr(self.args, role + "_sha"),
                "vcs.modified": "false",
            }
        self.container = {
            "Id": "fake-container-id", "Name": "/fake-ether-container",
            "Image": "sha256:fake-image-id",
            "Mounts": [{"Destination": "/usr/local/bin/ether", "Type": "bind",
                        "RW": False, "Source": str(self.args.server_binary)}],
            "Config": {"Entrypoint": None, "Cmd": ["/usr/local/bin/ether"],
                       "Image": "fake-image:1", "Env": ["PRIVATE=must-not-be-recorded"]},
        }
        mock = patch.object(provenance.subprocess, "check_output", side_effect=self.command)
        self.commands = mock.start()
        self.addCleanup(mock.stop)

    def command(self, args, **kwargs):
        # Fail closed if production code attempts any other external operation.
        self.assertEqual(kwargs, {"text": True, "timeout": 30})
        if args[:3] == ("fake-go", "version", "-m") and len(args) == 4:
            settings = self.metadata[args[3]]
            return f"{args[3]}: go1.27.3\n" + "".join(
                f"\tbuild\t{key}={value}\n" for key, value in settings.items())
        self.assertEqual(args, ("docker", "inspect", self.args.container))
        return json.dumps([self.container])

    def assert_metadata_rejected(self, role, settings, message):
        path = str(getattr(self.args, role + "_binary"))
        original = self.metadata[path]
        self.metadata[path] = settings
        self.commands.reset_mock()
        try:
            with self.assertRaisesRegex(ValueError, message):
                provenance.preflight(self.args)
            self.assertFalse(any(call.args[0][0] == "docker"
                                 for call in self.commands.call_args_list),
                             "invalid binaries must fail before container inspection")
        finally:
            self.metadata[path] = original

    def test_clean_control_records_exact_binaries(self):
        result = provenance.preflight(self.args)
        for role in ("server", "harness"):
            path = getattr(self.args, role + "_binary")
            self.assertEqual(result[role]["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertEqual(result[role]["build"]["vcs.revision"], getattr(self.args, role + "_sha"))
            self.assertEqual(result[role]["go"], "go1.27.3")
        self.assertEqual(result["container"]["image_id"], "sha256:fake-image-id")
        self.assertNotIn("must-not-be-recorded", json.dumps(result))
        self.assertEqual(self.commands.call_count, 3)

    def test_dirty_server_and_harness_rejected(self):
        for role in ("server", "harness"):
            with self.subTest(role=role):
                settings = dict(self.metadata[str(getattr(self.args, role + "_binary"))])
                settings["vcs.modified"] = "true"
                self.assert_metadata_rejected(role, settings, "dirty or missing vcs.modified")

    def test_missing_vcs_metadata_rejected(self):
        for role in ("server", "harness"):
            for missing in ("vcs", "vcs.revision", "vcs.modified", "all"):
                with self.subTest(role=role, missing=missing):
                    settings = dict(self.metadata[str(getattr(self.args, role + "_binary"))])
                    if missing == "all":
                        settings.clear()
                    else:
                        del settings[missing]
                    message = "dirty or missing vcs.modified" if missing == "vcs.modified" else "missing or unexpected VCS revision"
                    self.assert_metadata_rejected(role, settings, message)

    def test_wrong_server_and_harness_revision_rejected(self):
        for role in ("server", "harness"):
            with self.subTest(role=role):
                settings = dict(self.metadata[str(getattr(self.args, role + "_binary"))])
                settings["vcs.revision"] = "c" * 40
                self.assert_metadata_rejected(role, settings, "missing or unexpected VCS revision")

    def test_mount_mismatch_rejected(self):
        original = copy.deepcopy(self.container["Mounts"])
        variants = {
            "different source": [{**original[0], "Source": str(self.args.harness_binary)}],
            "writable": [{**original[0], "RW": True}],
            "volume instead of bind": [{**original[0], "Type": "volume"}],
            "wrong destination": [{**original[0], "Destination": "/usr/bin/ether"}],
            "absent": [],
            "ambiguous": original + original,
        }
        for name, mounts in variants.items():
            with self.subTest(mismatch=name):
                self.container["Mounts"] = mounts
                with self.assertRaisesRegex(ValueError, "container (mount does not match|must bind-mount)"):
                    provenance.preflight(self.args)


if __name__ == "__main__":
    unittest.main()
