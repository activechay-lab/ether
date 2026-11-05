import { referenceLooks, families } from "../pages/livetv/onair/fixtures";
import type { Look, Reel, OnAirAsset } from "../pages/livetv/onair/model";
export function createOnAirMock(tv: {
  handle: (
    method: string,
    seg: string[],
    q: URLSearchParams,
    body: unknown,
  ) => { body: unknown } | null;
}) {
  let looks = structuredClone(referenceLooks);
  let serial = 0;
  let reels: Reel[] = [
    {
      id: "saturday",
      name: "Saturday morning ads",
      order: "shuffle",
      repeatHours: 1,
      loudnessMatch: true,
      trimSilence: true,
      channelIds: ["v-movie-night"],
      clips: ["Turbo Crunch ’87", "Station promo", "Laser tag arena", "Mall opening"].map(
        (name, i) => ({
          id: "sat-" + i,
          name,
          durationMs: [30000, 15000, 30000, 60000][i]!,
          status: "ready",
        }),
      ),
    },
    {
      id: "promos",
      name: "Station promos",
      order: "sequence",
      repeatHours: 1,
      loudnessMatch: true,
      trimSilence: true,
      channelIds: [],
      clips: ["Tonight on Meridian", "The midnight movie"].map((name, i) => ({
        id: "promo-" + i,
        name,
        durationMs: 15000,
        status: "ready",
      })),
    },
  ];
  let assets: OnAirAsset[] = [
    {
      digest: "wordmark",
      name: "Station wordmark.svg",
      kind: "image",
      size: 18400,
      usedBy: ["Corner logo"],
    },
    {
      digest: "sting",
      name: "Midnight sting.wav",
      kind: "audio",
      size: 284000,
      usedBy: ["Ident", "Back from break"],
    },
  ];
  const renders = new Map<string, number>();
  const conversions = new Map<string, number>();
  return {
    handle(
      method: string,
      seg: string[],
      q: URLSearchParams,
      body: unknown,
    ): { body: unknown; status?: number } | null {
      if (seg[0] === "livetv" && seg[3] === "rundown") {
        return tv.handle(method, seg, q, body);
      }
      if (seg[0] !== "onair") return null;
      const [, kind, id, action, clipId] = seg;
      const data = body as Look & Reel;
      if (kind === "families") return { body: families };
      if (kind === "bindings") {
        const channels = tv.handle("GET", ["livetv", "channels"], q, null)?.body as {
          id: string;
          name: string;
        }[];
        const channel = channels?.find((c) => c.id === q.get("channel")) || channels?.[0];
        const qs = new URLSearchParams({
          from: q.get("at") || new Date().toISOString(),
          until: new Date(
            Date.parse(q.get("at") || "") + 86400000 || Date.now() + 86400000,
          ).toISOString(),
        });
        const guide = (
          (tv.handle("GET", ["livetv", "guide"], qs, null)?.body as {
            channelId: string;
            start: string;
            item: { title: string };
          }[]) || []
        )
          .filter((p) => p.channelId === channel?.id)
          .slice(0, 4);
        return {
          body: {
            channel: { name: channel?.name || "Your TV" },
            viewer: { name: q.get("viewer") === "2" ? "Alex" : "Braedon" },
            now: { title: guide[0]?.item.title || "Severance" },
            next: {
              title: guide[1]?.item.title || "Arrival",
              reason: "Because you enjoyed Severance",
            },
            list: guide.map((p) => ({
              title: p.item.title,
              time: new Date(p.start).toLocaleTimeString("en", {
                hour: "numeric",
                minute: "2-digit",
              }),
            })),
          },
        };
      }
      if (kind === "looks") {
        if (action === "duplicate" || action === "detach") {
          const l = looks.find((x) => x.id === id);
          if (!l) return { body: null, status: 404 };
          const copy = {
            ...structuredClone(l),
            id: "look-" + ++serial,
            name: l.name + " copy",
            pack: undefined,
            channelIds: [],
          };
          looks.push(copy);
          return { body: copy };
        }
        if (method === "GET") {
          const result = id ? looks.find((x) => x.id === id) : looks;
          for (const l of looks) {
            const start = renders.get(l.id);
            if (start)
              l.render = { done: Math.min(7, Math.floor((Date.now() - start) / 600)), total: 7 };
          }
          return { body: result };
        }
        if (method === "DELETE") {
          looks = looks.filter((x) => x.id !== id);
          return { body: null };
        }
        const l = { ...data, id: id || "look-" + ++serial };
        looks = looks.filter((x) => x.id !== l.id);
        looks.push(l);
        renders.set(l.id, Date.now());
        return { body: l };
      }
      if (kind === "reels") {
        const reel = reels.find((r) => r.id === id);
        if (action === "clips" && reel) {
          if (method === "POST") {
            const file = body instanceof FormData ? (body.get("file") as File) : null;
            const clip = {
              id: "clip-" + ++serial,
              name: file?.name || "Uploaded clip",
              durationMs: 30000,
              status: "converting" as const,
              progress: 0,
            };
            reel.clips.push(clip);
            conversions.set(clip.id, Date.now());
            return { body: clip };
          }
          if (method === "DELETE") {
            reel.clips = reel.clips.filter((c) => c.id !== clipId);
            return { body: null };
          }
          if (method === "PUT") {
            Object.assign(
              reel.clips.find((c) => c.id === clipId)!,
              body,
            );
            return { body: reel.clips.find((c) => c.id === clipId) };
          }
          return { body: { url: "/mock/clip.mp4" } };
        }
        for (const r of reels)
          for (const c of r.clips) {
            const start = conversions.get(c.id);
            if (start) {
              c.progress = Math.min(100, Math.floor((Date.now() - start) / 60));
              c.status = c.progress === 100 ? "ready" : "converting";
            }
          }
        if (method === "GET") return { body: id ? reel : reels };
        if (method === "DELETE") {
          reels = reels.filter((r) => r.id !== id);
          return { body: null };
        }
        const r = { ...data, id: id || "reel-" + ++serial };
        reels = reels.filter((x) => x.id !== r.id);
        reels.push(r);
        return { body: r };
      }
      if (kind === "assets") {
        if (method === "GET") return { body: id ? assets.find((a) => a.digest === id) : assets };
        if (method === "DELETE") {
          assets = assets.filter((a) => a.digest !== id);
          return { body: null };
        }
        const file = body instanceof FormData ? (body.get("file") as File) : null;
        const a = {
          digest: "asset-" + ++serial,
          name: file?.name || "Upload",
          kind: body instanceof FormData ? String(body.get("kind")) : "image",
          size: file?.size || 0,
          usedBy: [],
        };
        assets.push(a);
        return { body: a };
      }
      return { body: null, status: 404 };
    },
  };
}
