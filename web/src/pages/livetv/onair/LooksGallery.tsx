import { useEffect, useState } from "react";
import { Badge, Button } from "@braedonsaunders/appkit-ui";
import { useLocation, useSearchParams } from "react-router-dom";
import { Drawer } from "../../../components/Drawer";
import { t, tn } from "../../../i18n";
import { onair, type Reel, type Bindings } from "./model";
import { studio, type Style, type Template } from "./studio-model";
import { StyleThumbnail } from "./editor/RemixPanel";
import { LookDrawer } from "./LookDrawer";
import { ReelDrawer } from "./ReelDrawer";
import { ScreensDefaults } from "./ScreensDefaults";
import { errorMessage } from "../../../lib/api";
export function useLooks() {
  const location = useLocation(),
    [params] = useSearchParams();
  const [looks, setLooks] = useState<Style[]>([]),
    [reels, setReels] = useState<Reel[]>([]);
  const reload = () => {
    void studio.styles().then(setLooks);
    void onair.reels().then(setReels);
  };
  useEffect(reload, [location.pathname, params.get("channel"), params.get("look")]);
  return { looks, reels, reload };
}
function useTemplates() {
  const [templates, setTemplates] = useState<Template[]>([]);
  useEffect(() => {
    void studio.templates().then(setTemplates);
  }, []);
  return templates;
}
function useSlots(channel?: string) {
  const [b, setB] = useState<Bindings>({});
  useEffect(() => {
    void onair.bindings({ channel }).then(setB);
  }, [channel]);
  return {
    "channel.name": b.channel?.name ?? "",
    "next.title": b.next?.title ?? "",
    "next.poster": b.list?.[0]?.poster ?? "",
    "next.time": b.next?.time ?? "",
    "now.title": b.now?.title ?? "",
    "viewer.name": b.viewer?.name ?? "",
  };
}
export function blankStyle(): Style {
  return {
    id: "",
    name: t("Untitled style"),
    template: "",
    brand: {
      palette: { background: "#101827", accent: "#65d7ee", text: "#ffffff", glow: "#b9a4ff" },
      type: { display: "inter", body: "inter" },
      tagline: "",
      energy: 0.5,
      textures: [],
      language: "en",
    },
    roles: { ident: { duration: 8 } },
    scenes: [
      {
        id: crypto.randomUUID(),
        name: t("Ident"),
        role: "ident",
        enabled: true,
        weight: 1,
        document: { v: 1, moment: "ident", duration: 8, poster: 4, layers: [] },
        sounds: [],
      },
    ],
    controls: [],
    channelIds: [],
  };
}
export function LooksAside({
  looks,
  query,
  onSelect,
}: {
  looks: Style[];
  reels: Reel[];
  query: string;
  onSelect(kind: string, id: string): void;
}) {
  const slots = useSlots();
  return (
    <div className="space-y-3 p-3">
      {looks
        .filter((l) => l.name.toLowerCase().includes(query.toLowerCase()))
        .map((l) => (
          <button
            key={l.id}
            className="w-full rounded-lg border border-border p-2 text-left"
            onClick={() => onSelect("look", l.id)}
          >
            <StyleThumbnail style={l} slots={slots} />
            <p className="truncate text-sm">{l.name}</p>
            <p className="text-xs text-fg-muted">
              {tn(
                l.channelCount ?? l.channelIds.length,
                "Used by {count} channel",
                "Used by {count} channels",
              )}
            </p>
          </button>
        ))}
    </div>
  );
}
export function LooksGallery({
  looks,
  onSelect,
}: {
  looks: Style[];
  onNew(): void;
  onSelect(id: string): void;
}) {
  const templates = useTemplates(),
    slots = useSlots(),
    [working, setWorking] = useState(false),
    [error, setError] = useState("");
  const create = async (style: Style) => {
    setWorking(true);
    try {
      const saved = await studio.createStyle({ ...structuredClone(style), id: "", channelIds: [] });
      onSelect(saved.id);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setWorking(false);
    }
  };
  return (
    <div className="space-y-8 p-4 sm:p-6">
      {error ? (
        <p role="alert" className="text-danger">
          {error}
        </p>
      ) : null}
      <section className="space-y-4">
        <h2 className="text-lg font-semibold">{t("Your styles")}</h2>
        <div className="grid gap-5 sm:grid-cols-2">
          {looks.map((l) => (
            <button
              key={l.id}
              className="overflow-hidden rounded-xl border border-border bg-surface text-left hover:border-primary"
              onClick={() => onSelect(l.id)}
            >
              <StyleThumbnail style={l} slots={slots} />
              <div className="p-4">
                <h3 className="font-semibold">{l.name}</h3>
                <p className="text-xs text-fg-muted">
                  {tn(
                    l.channelCount ?? l.channelIds.length,
                    "Used by {count} channel",
                    "Used by {count} channels",
                  )}
                </p>
                {l.pack ? <Badge>{t("From {pack}", { pack: l.pack.name })}</Badge> : null}
              </div>
            </button>
          ))}
        </div>
      </section>
      <section className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold">{t("Make it yours")}</h2>
          <Button disabled={working} onClick={() => void create(blankStyle())}>
            {t("Start from blank")}
          </Button>
        </div>
        <p className="text-sm text-fg-muted">
          {t("Choose a starting point. Every colour, layer and movement is yours to change.")}
        </p>
        <div className="grid gap-5 sm:grid-cols-2">
          {templates.map((template) => (
            <button
              key={template.id}
              disabled={working}
              className="overflow-hidden rounded-xl border border-border bg-surface text-left hover:border-primary"
              onClick={() => void create(template.style)}
            >
              <StyleThumbnail style={template.style} slots={slots} />
              <div className="space-y-2 p-4">
                <h3 className="font-semibold">{template.name}</h3>
                <p className="text-sm text-fg-muted">{template.blurb}</p>
                <div className="flex flex-wrap gap-1">
                  {template.tags.map((tag) => (
                    <Badge key={tag}>{t(tag)}</Badge>
                  ))}
                </div>
              </div>
            </button>
          ))}
        </div>
      </section>
      <ScreensDefaults />
    </div>
  );
}
export function LookSurfaces({ looks, reload }: { looks: Style[]; reload(): void }) {
  const [params, setParams] = useSearchParams();
  const selected = looks.find((l) => l.id === params.get("look"));
  const close = () => {
    const next = new URLSearchParams(params);
    next.delete("look");
    next.delete("reel");
    setParams(next);
  };
  return (
    <>
      {params.get("look") === "new" ? (
        <Drawer open size="2xl" title={t("New style")} onClose={close}>
          <LooksGallery
            looks={looks}
            onNew={() => {}}
            onSelect={(id) => {
              reload();
              setParams((p) => {
                p.set("look", id);
                return p;
              });
            }}
          />
        </Drawer>
      ) : null}
      {selected ? (
        <LookDrawer
          key={selected.id}
          look={selected}
          initialTab="remix"
          stacked={params.has("channel")}
          onClose={close}
          onSaved={() => reload()}
        />
      ) : null}
      {params.has("reel") ? (
        <ReelDrawer open reelId={params.get("reel") || "new"} onClose={close} onSaved={reload} />
      ) : null}
    </>
  );
}
