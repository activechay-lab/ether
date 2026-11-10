"""Content-kind conformance: replay sdk/conformance/kinds/fixture.json."""

import copy
import json
import pathlib
import unittest

from ether_plugin import (
    CatalogProvider,
    FrontendAdapter,
    KindMatcher,
    KindResolver,
    SetupTester,
    candidate_ref,
    create_mock_host_client,
    parse_feed,
    plugin,
)

FIXTURE = json.loads(
    (pathlib.Path(__file__).parent / ".." / "conformance" / "kinds" / "fixture.json").read_text()
)


def _page(items, req):
    items = sorted(items, key=lambda it: it.get("index", 0))
    limit = int(req.get("limit") or 200)
    off = int(req.get("cursor") or 0)
    out = {"items": items[off:off + limit]}
    if off + limit < len(items):
        out["nextCursor"] = str(off + limit)
    return out


class Dataset(CatalogProvider, KindResolver, KindMatcher):
    def __init__(self, items):
        self.items = items

    def by_id(self, item_id):
        return next((it for it in self.items if it["id"] == item_id), None)

    def browse(self, ctx, req):
        return _page([it for it in self.items
                      if it["level"] == req.get("level") and it.get("parentId", "") == (req.get("parentId") or "")], req)

    def search(self, ctx, req):
        q = str(req.get("query") or "").lower()
        return _page([it for it in self.items if q in it["title"].lower()], req)

    def detail(self, ctx, kind, item_id):
        return copy.deepcopy(self.by_id(item_id))

    def changes(self, ctx, req):
        return {"upserts": copy.deepcopy(self.items), "complete": True}

    def resolve(self, ctx, req):
        item, hints = req["item"], req["item"].get("hints") or {}
        sources = []
        parent = self.by_id(item.get("parentId", ""))
        if parent and hints.get("member"):
            ph = parent.get("hints") or {}
            sources.append({
                "id": "zip", "pattern": "container", "mime": "audio/flac",
                "container": {"format": "zip",
                              "volumes": [{"url": ph["zipUrl"], "size": ph["zipSize"]}],
                              "member": hints["member"]},
            })
        if hints.get("pageUrl"):
            sources.append({"id": "direct", "pattern": "range", "mime": "audio/flac", "url": hints["pageUrl"]})
        return {"sources": sources}

    def match(self, ctx, req):
        matches = []
        for c in req.get("candidates") or []:
            hit = req["item"]["title"].lower() in str(c.get("title", "")).lower()
            matches.append({"candidateRef": candidate_ref(c),
                            "score": 0.95 if hit else 0.1,
                            "reasons": ["Title matches" if hit else "Title does not match"]})
        return {"matches": sorted(matches, key=lambda m: -m["score"])}


class Setup(SetupTester):
    def test_setup(self, ctx, req):
        if (req.get("fields") or {}).get("apiKey"):
            return {"ok": True, "detail": "Connected"}
        return {"ok": False, "detail": "Add your API key first", "fields": {"apiKey": "Required"}}


class Feed(FrontendAdapter):
    def handle(self, method, path, headers, body):
        return 200, {"Content-Type": "application/atom+xml"}, '<?xml version="1.0"?><feed><title>Field Recordings</title></feed>'


class KindsConformanceTest(unittest.TestCase):
    def test_fixture_replay(self):
        inst = plugin(
            manifest={"id": "community.example.recordings", "name": "Field Recordings", "version": "1.0.0",
                      "ether": {"protocol": 1, "kind": "content", "summary": "Fixture provider",
                                "runtime": {"kind": "remote"},
                                "capabilities": ["catalog_provider", "kind_resolver", "matcher", "frontend_adapter"],
                                "contributes": {"mediaKinds": [FIXTURE["kind"]]}}},
            host=create_mock_host_client(),
        )
        ds = Dataset(FIXTURE["items"])
        inst.on_catalog("recordings", ds)
        inst.on_resolve("recordings", ds)
        inst.on_match("recordings", ds)
        inst.on_setup_test(Setup())
        inst.on_frontend("feed", Feed())

        for call in FIXTURE["calls"]:
            with self.subTest(call=call["name"]):
                resp = inst.dispatch({"method": call["method"], "path": call["path"],
                                      "body": json.dumps(call.get("body") or {})})
                self.assertEqual(resp["status"], call["expectStatus"], resp)
                body = resp["body"]
                if "expect" in call:
                    if isinstance(body, (str, bytes)):
                        body = json.loads(body)
                    self.assertEqual(body, call["expect"])
                elif "expectIds" in call:
                    self.assertEqual([it["id"] for it in body["upserts"]], call["expectIds"])
                    self.assertEqual(body["complete"], call["expectComplete"])
                elif "expectTopRef" in call:
                    self.assertEqual(body["matches"][0]["candidateRef"], call["expectTopRef"])
                elif "expectBodyContains" in call:
                    self.assertIn(call["expectBodyContains"], str(body))

    def test_mock_host_kinds_and_feed(self):
        host = create_mock_host_client()
        self.assertEqual(host.kinds.upsert("recordings", [{"id": "a", "title": "A"}]), 1)
        self.assertEqual(len(host.kinds.items("recordings")["items"]), 1)
        ref = host.cache.put("pk-x-recordings", "a.flac", b"bytes")
        self.assertTrue(ref.startswith("blob:"))
        self.assertEqual(host.cache.open(ref), b"bytes")
        host.kinds.delete("recordings", ["a"])
        self.assertEqual(host.kinds.items("recordings")["items"], [])
        feed = parse_feed('<rss><channel><title>T</title><item><title>E</title><guid>g1</guid><enclosure url="https://x/e.mp3"/></item></channel></rss>')
        self.assertEqual(feed["entries"][0]["audio"], "https://x/e.mp3")


if __name__ == "__main__":
    unittest.main()
