import { canonicalRating, NOT_RATED, RATING_LEVELS } from "../lib/contentRatings";
import type { TitleCard } from "../lib/contract";
import type {
  Play,
  Preroll,
  PrerollInput,
  PrerollRules,
  Selection,
  Simulation,
} from "../pages/playback/prerolls-api";

/** What the simulator needs to know about a feature title. */
type PrerollTitle = Pick<TitleCard, "key" | "type" | "title" | "genres" | "contentRating">;

type Options = {
  /** Seed three pre-rolls, their recent plays and one video still converting. */
  seed?: boolean;
  /** Catalogue lookup for simulated features. */
  title?: (ratingKey: string) => PrerollTitle | undefined;
  /** Viewer names for the activity list. */
  viewerName?: (id: number) => string;
  /** Collection membership for the collection rule. */
  inCollection?: (collectionId: string, ratingKey: string) => boolean;
  now?: () => Date;
};

type LoggedPlay = Play & { prerollId: string };

const blankRules = (): PrerollRules => ({
  kinds: ["movie"],
  firstEpisodeOnly: false,
  bingeGapMinutes: 30,
  genres: [],
  excludeGenres: [],
  collections: [],
  maxContentRating: "",
  dateFrom: "",
  dateTo: "",
  audience: { mode: "everyone", viewers: [] },
  network: "any",
  devices: [],
});

const HUES: Record<string, [string, string, string]> = {
  "Halloween sting": ["#1a0b02", "#c2410c", "#fb923c"],
  "Family welcome": ["#0c1a2e", "#2563eb", "#facc15"],
  "Cinema intro": ["#0b0b12", "#7c3aed", "#e879f9"],
};

/** A 16:9 frame for a pre-roll's thumbnail, drawn from its name. */
function frameSvg(name: string): string {
  const [bg, mid, hi] = HUES[name] ?? ["#0f172a", "#0f766e", "#5eead4"];
  const safe = name.replace(/[<>&"]/g, "");
  return `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360" viewBox="0 0 640 360">
<defs><radialGradient id="g" cx="50%" cy="45%" r="75%"><stop offset="0" stop-color="${mid}"/><stop offset="1" stop-color="${bg}"/></radialGradient>
<linearGradient id="v" x1="0" y1="0" x2="0" y2="1"><stop offset="0.6" stop-color="#000" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity="0.65"/></linearGradient></defs>
<rect width="640" height="360" fill="url(#g)"/>
<circle cx="470" cy="120" r="120" fill="${hi}" opacity="0.18"/><circle cx="150" cy="260" r="160" fill="${hi}" opacity="0.1"/>
<rect width="640" height="360" fill="url(#v)"/>
<text x="320" y="196" text-anchor="middle" font-family="Georgia, serif" font-size="46" font-weight="700" fill="#fff" letter-spacing="3">${safe.toUpperCase()}</text>
<rect x="250" y="218" width="140" height="3" fill="${hi}"/>
</svg>`;
}

const ratingIndex = (v: string) => RATING_LEVELS.findIndex((l) => l.value === canonicalRating(v));

/** "10-31" for a moment in local time. */
const monthDay = (d: Date) =>
  `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

function inDateWindow(at: Date, from: string, to: string): boolean {
  const md = monthDay(at);
  const end = to || from;
  return from <= end ? md >= from && md <= end : md >= from || md <= end;
}

/**
 * Isolated owner API fake for the pre-roll console. The simulator runs the
 * server's rule order and explains every condition, so the console's
 * reasons read exactly as they will against a real server.
 */
export function createPrerollsMock(options: Options = {}) {
  const now = options.now ?? (() => new Date());
  const nameOf = options.viewerName ?? ((id: number) => `Viewer ${id}`);
  let selection: Selection = "priority";
  let next = 1;
  let items: Preroll[] = [];
  let plays: LoggedPlay[] = [];
  /** Videos still converting, with the list reads left before they finish. */
  const converting = new Map<string, number>();
  const stamp = () => now().toISOString();
  const urls = (id: string, version: string) => ({
    thumbUrl: `/ether/api/admin/prerolls/${id}/thumb?v=${encodeURIComponent(version)}`,
    previewUrl: `/ether/api/admin/prerolls/${id}/preview?v=${encodeURIComponent(version)}`,
  });
  const make = (input: PrerollInput, video: Partial<Preroll["video"]> = {}): Preroll => {
    const id = `pr_${next++}`;
    const at = stamp();
    return {
      ...structuredClone(input),
      id,
      video: {
        state: "",
        durationMs: 0,
        width: 0,
        height: 0,
        thumbUrl: "",
        previewUrl: "",
        problem: "",
        source: "upload",
        ...video,
      },
      plays30d: 0,
      createdAt: at,
      updatedAt: at,
    };
  };
  const ready = (durationMs: number, source: "upload" | "path", version: string, id: string) => ({
    state: "ready" as const,
    durationMs,
    width: 1920,
    height: 1080,
    problem: "" as const,
    source,
    ...urls(id, version),
  });

  if (options.seed) {
    const seeds: [PrerollInput, number, "ready" | "processing"][] = [
      [
        {
          name: "Halloween sting",
          enabled: true,
          rules: {
            ...blankRules(),
            genres: ["Horror"],
            dateFrom: "10-01",
            dateTo: "10-31",
            audience: { mode: "except", viewers: [3] },
          },
          frequency: { mode: "daily", hours: 6 },
          priority: 10,
          order: "before_trailers",
        },
        9500,
        "ready",
      ],
      [
        {
          name: "Family welcome",
          enabled: true,
          rules: {
            ...blankRules(),
            kinds: ["movie", "episode"],
            firstEpisodeOnly: true,
            maxContentRating: "PG",
            audience: { mode: "only", viewers: [3] },
          },
          frequency: { mode: "always", hours: 6 },
          priority: 5,
          order: "after_trailers",
        },
        14200,
        "ready",
      ],
      [
        {
          name: "Cinema intro",
          enabled: true,
          rules: { ...blankRules(), network: "home", devices: ["tv"] },
          frequency: { mode: "hours", hours: 6 },
          priority: 0,
          order: "before_trailers",
        },
        21000,
        "processing",
      ],
    ];
    items = seeds.map(([input, ms, state]) => {
      const p = make(input);
      p.video =
        state === "ready"
          ? ready(ms, "upload", p.updatedAt, p.id)
          : { ...p.video, state: "processing", source: "path" };
      return p;
    });
    items[0]!.plays30d = 41;
    items[1]!.plays30d = 18;
    const ago = (minutes: number) => new Date(now().getTime() - minutes * 60_000).toISOString();
    plays = [
      [1, "Hollow Harbor", "1000004", 25, "Plex for Apple TV"],
      [2, "The Quiet Signal", "1000011", 190, "Plex Web"],
      [1, "Night Orchard", "1000018", 60 * 26, "Plex for Android"],
    ].map(([viewerId, title, ratingKey, minutes, client]) => ({
      prerollId: items[0]!.id,
      viewerId: viewerId as number,
      viewerName: nameOf(viewerId as number),
      title: title as string,
      ratingKey: ratingKey as string,
      at: ago(minutes as number),
      client: client as string,
    }));
  }

  /** The server's fixed rule order; `check` records each condition. */
  function rules(
    p: Preroll,
    req: {
      viewerId: number;
      feature?: PrerollTitle;
      device: string;
      away: boolean;
      resume: boolean;
      at: Date;
    },
    check: (code: string, pass: () => boolean) => void,
  ) {
    const r = p.rules;
    const kind =
      req.feature?.type === "movie"
        ? "movie"
        : req.feature?.type === "episode" || req.feature?.type === "show"
          ? "episode"
          : "";
    const genres = (req.feature?.genres ?? []).map((g) => g.toLowerCase());
    const anyGenre = (list: string[]) => list.some((g) => genres.includes(g.toLowerCase()));
    const seen = plays.filter((x) => x.prerollId === p.id && x.viewerId === req.viewerId);
    check("disabled", () => p.enabled);
    check("not_ready", () => p.video.state === "ready");
    check("resumed", () => !req.resume);
    check("kind", () => kind !== "" && r.kinds.includes(kind));
    if (kind === "episode" && r.firstEpisodeOnly) {
      check("first_episode", () => true);
      if (r.bingeGapMinutes > 0)
        check("binge_gap", () => {
          const since = req.at.getTime() - r.bingeGapMinutes * 60_000;
          return !plays.some(
            (x) =>
              x.viewerId === req.viewerId &&
              Date.parse(x.at) >= since &&
              Date.parse(x.at) <= req.at.getTime(),
          );
        });
    }
    if (r.genres.length) check("genre", () => anyGenre(r.genres));
    if (r.excludeGenres.length) check("excluded_genre", () => !anyGenre(r.excludeGenres));
    if (r.collections.length)
      check("collection", () =>
        r.collections.some((id) => options.inCollection?.(id, req.feature?.key ?? "") ?? false),
      );
    if (r.maxContentRating)
      check("rating", () => {
        const have = canonicalRating(req.feature?.contentRating ?? NOT_RATED);
        const limit = ratingIndex(r.maxContentRating);
        const level = ratingIndex(have);
        return level >= 0 && limit >= 0 && level <= limit;
      });
    if (r.dateFrom) check("date_window", () => inDateWindow(req.at, r.dateFrom, r.dateTo));
    if (r.audience.mode !== "everyone")
      check(
        "audience",
        () => r.audience.viewers.includes(req.viewerId) === (r.audience.mode === "only"),
      );
    if (r.network !== "any") check("network", () => req.away === (r.network === "away"));
    if (r.devices.length) check("device", () => r.devices.includes(req.device as never));
    if (p.frequency.mode !== "always")
      check("frequency", () => {
        const at = req.at.getTime();
        const since =
          p.frequency.mode === "daily"
            ? new Date(req.at.getFullYear(), req.at.getMonth(), req.at.getDate()).getTime()
            : p.frequency.mode === "hours"
              ? at - p.frequency.hours * 3_600_000
              : -Infinity;
        return !seen.some(
          (x) =>
            (p.frequency.mode !== "title" || x.ratingKey === req.feature?.key) &&
            Date.parse(x.at) >= since &&
            Date.parse(x.at) <= at,
        );
      });
  }

  function simulate(body: {
    viewerId: number;
    ratingKey: string;
    device?: string;
    away?: boolean;
    resume?: boolean;
    at?: string;
  }): Simulation {
    const req = {
      viewerId: Number(body.viewerId),
      feature: options.title?.(String(body.ratingKey)),
      device: body.device ?? "tv",
      away: Boolean(body.away),
      resume: Boolean(body.resume),
      at: body.at ? new Date(body.at) : now(),
    };
    const candidates: Simulation["candidates"] = items.map((p) => {
      const reasons: { code: string; ok: boolean }[] = [];
      rules(p, req, (code, pass) => reasons.push({ code, ok: pass() }));
      return { id: p.id, name: p.name, plays: reasons.every((r) => r.ok), reasons };
    });
    const matches = candidates.flatMap((c, i) => (c.plays ? [i] : []));
    if (!matches.length) return { winner: null, candidates };
    let pick = matches[0]!;
    if (selection === "shuffle") pick = matches[Math.floor(Math.random() * matches.length)]!;
    else for (const m of matches) if (items[m]!.priority > items[pick]!.priority) pick = m;
    for (const m of matches) {
      if (m === pick) continue;
      const c = candidates[m]!;
      c.plays = false;
      c.reasons.push({
        code:
          selection !== "shuffle" && items[m]!.priority < items[pick]!.priority
            ? "lower_priority"
            : "shuffled_out",
        ok: false,
      });
    }
    return { winner: { id: items[pick]!.id, name: items[pick]!.name }, candidates };
  }

  /** A converting video finishes after a few list reads, as a worker would. */
  function advance() {
    for (const [id, left] of converting) {
      const p = items.find((x) => x.id === id);
      if (!p) {
        converting.delete(id);
        continue;
      }
      if (left > 1) {
        converting.set(id, left - 1);
        continue;
      }
      converting.delete(id);
      p.updatedAt = stamp();
      p.video = ready(12_000, p.video.source, p.updatedAt, p.id);
    }
  }

  const input = (body: Partial<PrerollInput>, base?: Preroll): PrerollInput => ({
    name: String(body.name ?? base?.name ?? "").trim(),
    enabled: body.enabled ?? base?.enabled ?? true,
    rules: { ...blankRules(), ...base?.rules, ...body.rules },
    frequency: body.frequency ?? base?.frequency ?? { mode: "always", hours: 6 },
    priority: body.priority ?? base?.priority ?? 0,
    order: body.order ?? base?.order ?? "before_trailers",
  });

  return async (url: URL, init: RequestInit = {}): Promise<Response> => {
    const path = url.pathname.replace(/^\/ether\/api\/admin\/prerolls/, "");
    const method = (init.method ?? "GET").toUpperCase();
    const body =
      typeof init.body === "string" && init.body ? (JSON.parse(init.body) as never) : ({} as never);
    const reply = (data: unknown, status = 200) =>
      new Response(JSON.stringify(data), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    const invalid = (field: string, message: string) =>
      reply({ error: { code: "validation", message, fields: [{ field, message }] } }, 400);
    if (!path && method === "GET") {
      advance();
      return reply({ prerolls: items, selection });
    }
    if (!path && method === "POST") {
      const data = input(body);
      if (!data.name) return invalid("name", "name is required");
      const entry = make(data);
      items.push(entry);
      return reply(entry);
    }
    if (path === "/selection" && method === "PUT") {
      selection = (body as { selection: Selection }).selection;
      return reply({ selection });
    }
    if (path === "/import/pms" && method === "POST") {
      const groups = String((body as { paths?: string }).paths ?? "").split(";");
      // As on the server: earlier semicolon groups win, commas share a priority.
      const created = groups.flatMap((group, gi) =>
        group
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)
          .map((file) => {
            const p = make(
              {
                name: (file.split(/[\\/]/).pop() || file).replace(/\.[a-z0-9]+$/i, ""),
                enabled: true,
                rules: blankRules(),
                frequency: { mode: "always", hours: 6 },
                priority: (groups.length - gi) * 10,
                order: "before_trailers",
              },
              { state: "processing", source: "path" },
            );
            converting.set(p.id, 2);
            return p;
          }),
      );
      if (!created.length) return invalid("paths", "no video paths found");
      items.push(...created);
      return reply({ created });
    }
    if (path === "/simulate" && method === "POST") return reply(simulate(body));
    const [, id, action] = path.split("/");
    const index = items.findIndex((p) => p.id === id);
    const current = items[index];
    if (!current) return reply({ error: { code: "not_found", message: "no such pre-roll" } }, 404);
    if (action === "thumb")
      return current.video.state === "ready"
        ? new Response(frameSvg(current.name), { headers: { "Content-Type": "image/svg+xml" } })
        : reply({ error: { code: "not_found", message: "no thumbnail yet" } }, 404);
    if (action === "preview")
      return reply({ error: { code: "not_found", message: "no preview in the mock" } }, 404);
    if (action === "plays")
      return reply({
        plays: plays
          .filter((x) => x.prerollId === id)
          .slice(0, Number(url.searchParams.get("limit") ?? 50))
          .map(({ prerollId: _, ...play }) => play),
      });
    if (action === "video" && method === "POST") {
      const fromPath = !(init.body instanceof FormData);
      const file = fromPath ? String((body as { path?: string }).path ?? "") : "";
      if (fromPath && !file.startsWith("/"))
        return invalid("path", "path must be absolute on the server");
      current.updatedAt = stamp();
      current.video = {
        ...current.video,
        state: "processing",
        problem: "",
        source: fromPath ? "path" : "upload",
        thumbUrl: "",
        previewUrl: "",
      };
      converting.set(current.id, 2);
      return reply(current);
    }
    if (!action && method === "PUT") {
      const data = input(body, current);
      if (!data.name) return invalid("name", "name is required");
      items[index] = { ...current, ...data, updatedAt: stamp() };
      return reply(items[index]);
    }
    if (!action && method === "DELETE") {
      items.splice(index, 1);
      plays = plays.filter((x) => x.prerollId !== id);
      return reply({});
    }
    return reply({ error: { code: "not_found", message: "no such route" } }, 404);
  };
}
