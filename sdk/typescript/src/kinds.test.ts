import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  candidateRef,
  createMockHostClient,
  parseFeed,
  plugin,
  type CatalogProvider,
  type KindItem,
  type KindMatcher,
  type KindResolver,
} from "./index.ts";

const fixture = JSON.parse(
  readFileSync(new URL("../../conformance/kinds/fixture.json", import.meta.url), "utf8"),
) as {
  kind: Record<string, unknown>;
  items: KindItem[];
  calls: Array<Record<string, any>>;
};

function pageOf(items: KindItem[], req: { cursor?: string; limit?: number }) {
  const sorted = [...items].sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
  const limit = req.limit || 200;
  const off = Number(req.cursor || 0);
  const page: { items: KindItem[]; nextCursor?: string } = { items: sorted.slice(off, off + limit) };
  if (off + limit < sorted.length) page.nextCursor = String(off + limit);
  return page;
}

class Dataset implements CatalogProvider, KindResolver, KindMatcher {
  items: KindItem[];
  constructor(items: KindItem[]) { this.items = items; }
  browse(req: any) {
    return pageOf(this.items.filter((it) => it.level === req.level && (it.parentId ?? "") === (req.parentId ?? "")), req);
  }
  search(req: any) {
    const q = String(req.query ?? "").toLowerCase();
    return pageOf(this.items.filter((it) => it.title.toLowerCase().includes(q)), req);
  }
  detail(_kind: string, id: string) {
    return structuredClone(this.items.find((it) => it.id === id)!);
  }
  changes() {
    return { upserts: structuredClone(this.items), complete: true };
  }
  resolve(req: any) {
    const hints = req.item.hints ?? {};
    const parent = this.items.find((it) => it.id === req.item.parentId);
    const sources: any[] = [];
    if (parent && hints.member) {
      const ph = (parent.hints ?? {}) as Record<string, any>;
      sources.push({
        id: "zip", pattern: "container", mime: "audio/flac",
        container: { format: "zip", volumes: [{ url: ph.zipUrl, size: ph.zipSize }], member: hints.member },
      });
    }
    if (hints.pageUrl) sources.push({ id: "direct", pattern: "range", mime: "audio/flac", url: hints.pageUrl });
    return { sources };
  }
  match(req: any) {
    const matches = (req.candidates ?? []).map((c: any) => {
      const hit = String(c.title ?? "").toLowerCase().includes(req.item.title.toLowerCase());
      return { candidateRef: candidateRef(c), score: hit ? 0.95 : 0.1, reasons: [hit ? "Title matches" : "Title does not match"] };
    });
    return { matches: matches.sort((a: any, b: any) => b.score - a.score) };
  }
}

test("content-kind conformance fixture replay", async () => {
  const inst = plugin({
    manifest: {
      id: "community.example.recordings",
      name: "Field Recordings",
      version: "1.0.0",
      ether: {
        protocol: 1, kind: "content", summary: "Fixture provider",
        runtime: { kind: "remote" },
        capabilities: ["catalog_provider", "kind_resolver", "matcher", "frontend_adapter"],
        contributes: { mediaKinds: [fixture.kind as never] },
      },
    } as never,
    routes: {},
    host: createMockHostClient(),
  });
  const ds = new Dataset(fixture.items);
  inst.onCatalog("recordings", ds);
  inst.onResolve("recordings", ds);
  inst.onMatch("recordings", ds);
  inst.onSetupTest({
    testSetup: (req) =>
      req.fields?.apiKey
        ? { ok: true, detail: "Connected" }
        : { ok: false, detail: "Add your API key first", fields: { apiKey: "Required" } },
  });
  inst.onFrontend("feed", {
    handle: () => ({ status: 200, headers: { "Content-Type": "application/atom+xml" }, body: '<?xml version="1.0"?><feed><title>Field Recordings</title></feed>' }),
  });

  for (const call of fixture.calls) {
    const resp = await inst.dispatch({ method: call.method, path: call.path, body: JSON.stringify(call.body ?? {}) });
    assert.equal(resp.status, call.expectStatus, `${call.name}: ${JSON.stringify(resp.body)}`);
    if (call.expect) {
      assert.deepEqual(resp.body, call.expect, call.name);
    } else if (call.expectIds) {
      const body = resp.body as any;
      assert.deepEqual(body.upserts.map((it: KindItem) => it.id), call.expectIds);
      assert.equal(body.complete, call.expectComplete);
    } else if (call.expectTopRef) {
      assert.equal((resp.body as any).matches[0].candidateRef, call.expectTopRef);
    } else if (call.expectBodyContains) {
      assert.ok(String(resp.body).includes(call.expectBodyContains), call.name);
    }
  }
});

test("mock host kinds, cache and feed kit", async () => {
  const host = createMockHostClient();
  assert.equal(await host.kinds!.upsert("recordings", [{ id: "a", level: "track", title: "A" }]), 1);
  assert.equal((await host.kinds!.items("recordings")).items.length, 1);
  const ref = await host.cache!.put("pk-x-recordings", "a.flac", new TextEncoder().encode("bytes"));
  assert.ok(ref.startsWith("blob:"));
  assert.equal(new TextDecoder().decode(await host.cache!.open(ref)), "bytes");
  await host.kinds!.delete("recordings", ["a"]);
  assert.equal((await host.kinds!.items("recordings")).items.length, 0);
  const feed = parseFeed('<rss><channel><title>T</title><item><title>E</title><guid>g1</guid><enclosure url="https://x/e.mp3"/></item></channel></rss>');
  assert.equal(feed.entries[0]!.audio, "https://x/e.mp3");
});
