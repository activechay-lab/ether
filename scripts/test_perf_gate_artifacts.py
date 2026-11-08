"""Tiny synthetic JSON checks only; no gate, database, service or build."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("perf-gate-artifacts.py")


def trace():
    return {
        "tag": "cold|stremio|catalog search|1", "surface": "stremio", "method": "GET",
        "path": "/RAW_PATH_SECRET/catalog/movie/search.json", "status": 200,
        "bytes": 120, "start": "2026-11-08T12:00:00Z", "duration": 100,
        "queries": 1, "writes": 0, "rows": 7, "dbTime": 20, "lockWait": 0,
        "statements": [{"sql": "SELECT 'LITERAL_SECRET', \"QUOTED_SECRET\" FROM catalog_titles "
                       "/* BLOCK_SECRET */ WHERE key=? -- LINE_SECRET\n",
                       "site": "internal/catalog/store.go:1", "count": 1, "rows": 7, "time": 20}],
    }


class ArtifactTests(unittest.TestCase):
    def run_fixture(self, large, report=True):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private = root / "perf-gate-private"
            private.mkdir()
            (private / "small-trace.jsonl").write_text(json.dumps(trace()) + "\n")
            if large is not None:
                (private / "large-trace.jsonl").write_text(large)
            (private / "small.db").write_text("DATABASE_SECRET")
            (private / "config.json").write_text("CONFIG_SECRET")
            if report:
                (root / "perf-gate.md").write_text("| search | 7 rows | FAIL |\n")
            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(root)], capture_output=True, text=True,
                env={**os.environ, "DATABASE_URL": "ENV_SECRET"}, check=False,
            )
            output = root / "perf-gate-artifacts"
            files = {path.name: path.read_text() for path in output.iterdir()}
            for secret in ("RAW_PATH_SECRET", "LITERAL_SECRET", "QUOTED_SECRET", "BLOCK_SECRET",
                           "LINE_SECRET", "DATABASE_SECRET", "CONFIG_SECRET", "ENV_SECRET"):
                self.assertNotIn(secret, "".join(files.values()))
            self.assertNotIn("small.db", files)
            self.assertNotIn("config.json", files)
            return result.returncode, files, json.loads(files["provenance.json"])

    def test_sanitized_counters_sites_and_provenance(self):
        code, files, provenance = self.run_fixture(json.dumps(trace()) + "\n")
        self.assertEqual(code, 0)
        self.assertTrue(provenance["evidence_complete"])
        self.assertRegex(provenance["checkout_sha"], r"^[0-9a-f]{40}$")
        record = json.loads(files["large-trace.jsonl"])
        self.assertNotIn("path", record)
        self.assertEqual(record["tag"], trace()["tag"])
        self.assertEqual(record["rows"], 7)
        self.assertEqual(record["statements"][0]["rows"], 7)
        self.assertEqual(record["statements"][0]["site"], "internal/catalog/store.go:1")
        self.assertIn("catalog_titles", record["statements"][0]["sql"])

    def test_partial_malformed_keeps_valid_records_but_fails(self):
        code, files, provenance = self.run_fixture(json.dumps(trace()) + '\n{"unfinished":')
        self.assertNotEqual(code, 0)
        self.assertFalse(provenance["evidence_complete"])
        self.assertEqual(provenance["large"]["malformed_lines"], 1)
        self.assertEqual(len(files["large-trace.jsonl"].splitlines()), 1)

    def test_missing_empty_and_missing_report_fail(self):
        for large, report in ((None, True), ("", True), (json.dumps(trace()), False)):
            with self.subTest(large=large, report=report):
                code, files, provenance = self.run_fixture(large, report)
                self.assertNotEqual(code, 0)
                self.assertFalse(provenance["evidence_complete"])
                self.assertIn("small-trace.jsonl", files)

    def test_valid_json_with_invalid_shape_fails(self):
        bad = [{}, [], {**trace(), "statements": {}}, {**trace(), "statements": [1]},
               {**trace(), "rows": "7"}, {**trace(), "queries": True},
               {**trace(), "surface": ""}, {**trace(), "path": None},
               {**trace(), "statements": [{"sql": "SELECT 1"}]}]
        for record in bad:
            with self.subTest(record=record):
                code, files, provenance = self.run_fixture(json.dumps(record) + "\n")
                self.assertNotEqual(code, 0)
                self.assertFalse(provenance["evidence_complete"])
                self.assertEqual(provenance["large"]["invalid_records"], 1)
                self.assertEqual(files["large-trace.jsonl"], "")

    def test_untagged_only_is_not_complete(self):
        record = trace()
        del record["tag"]
        code, files, provenance = self.run_fixture(json.dumps(record))
        self.assertNotEqual(code, 0)
        self.assertEqual(provenance["large"]["records"], 1)
        self.assertEqual(provenance["large"]["tagged_records"], 0)


if __name__ == "__main__":
    unittest.main()
