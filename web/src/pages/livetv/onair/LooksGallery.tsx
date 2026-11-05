import { useEffect, useState } from "react";
import { Badge, Button, EmptyState } from "@braedonsaunders/appkit-ui";
import { Plus, Sparkles, Copy, Clapperboard, Palette } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { Drawer } from "../../../components/Drawer";
import { t, tn } from "../../../i18n";
import { onair, type Look, type Reel, type Family } from "./model";
import { LookTile } from "./Stage";
import { LookDrawer } from "./LookDrawer";
import { ReelDrawer } from "./ReelDrawer";
import { ScreensDefaults } from "./ScreensDefaults";
export function useLooks() {
  const [looks, setLooks] = useState<Look[]>([]),
    [reels, setReels] = useState<Reel[]>([]);
  const reload = () => {
    void onair.looks().then(setLooks);
    void onair.reels().then(setReels);
  };
  useEffect(reload, []);
  return { looks, reels, reload };
}
export function LooksAside({
  looks,
  reels,
  query,
  onSelect,
}: {
  looks: Look[];
  reels: Reel[];
  query: string;
  onSelect: (kind: string, id: string) => void;
}) {
  return (
    <div className="space-y-5 p-3">
      {looks
        .filter((l) => l.name.toLowerCase().includes(query.toLowerCase()))
        .map((l) => (
          <button
            key={l.id}
            className="flex w-full items-center gap-3 rounded-lg p-2 text-left hover:bg-surface-hover"
            onClick={() => onSelect("look", l.id)}
          >
            <div className="w-24 shrink-0">
              <LookTile look={l} playOnHover={false} />
            </div>
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{l.name}</p>
              <p className="text-xs text-fg-muted">{t(familyName(l.family))}</p>
              <p className="text-xs text-fg-subtle">
                {tn(l.channelIds.length, "Used by {count} channel", "Used by {count} channels")}
              </p>
              {l.pack ? <Badge>{t("From {pack}", { pack: l.pack.name })}</Badge> : null}
            </div>
          </button>
        ))}
      <div className="border-t border-border pt-4">
        <h3 className="mb-2 px-2 text-xs font-semibold uppercase tracking-wider text-fg-muted">
          {t("Reels")}
        </h3>
        {reels.map((r) => (
          <button
            key={r.id}
            className="flex w-full items-center gap-3 rounded-lg px-2 py-3 text-left hover:bg-surface-hover"
            onClick={() => onSelect("reel", r.id)}
          >
            <Clapperboard size={20} className="text-fg-muted" />
            <span className="flex-1 text-sm">
              {r.name}
              <span className="block text-xs text-fg-subtle">
                {tn(r.clips.length, "{count} clip", "{count} clips")}
              </span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
function familyName(id: string) {
  return (
    {
      kids: "Kids",
      lateNight: "Late night",
      cinema: "Cinema",
      retro: "Retro",
      personal: "Personal",
      clean: "Clean",
    }[id] || id
  );
}
export function LooksGallery({
  looks,
  onNew,
  onSelect,
}: {
  looks: Look[];
  onNew: () => void;
  onSelect: (id: string) => void;
}) {
  return (
    <div className="space-y-6 p-4 sm:p-6">
      <ScreensDefaults />
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">{t("Looks")}</h2>
        <span className="text-xs text-fg-muted">
          {tn(looks.length, "{count} look", "{count} looks")}
        </span>
      </div>
      {!looks.length ? (
        <EmptyState
          icon={<Palette />}
          title={t("Give your channels a look")}
          description={t("Choose a family to get started.")}
          action={<Button onClick={onNew}>{t("New look")}</Button>}
        />
      ) : null}
      <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 xl:grid-cols-2">
        {looks.map((l) => (
          <button
            key={l.id}
            className="group min-w-0 overflow-hidden rounded-xl border border-border bg-surface text-left shadow-sm transition-colors hover:border-primary"
            onClick={() => onSelect(l.id)}
          >
            <LookTile look={l} />
            <div className="flex items-center justify-between gap-3 p-4">
              <div>
                <h3 className="font-semibold tracking-tight">{l.name}</h3>
                <p className="mt-1 text-xs text-fg-muted">{t(familyName(l.family))}</p>
              </div>
              <span className="text-xs text-fg-subtle">
                {tn(l.channelIds.length, "Used by {count} channel", "Used by {count} channels")}
              </span>
            </div>
          </button>
        ))}
        <button
          onClick={onNew}
          className="flex min-h-48 flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border bg-surface/50 text-fg-muted hover:border-primary hover:text-primary"
        >
          <Plus size={26} />
          <span className="text-sm font-medium">{t("New look")}</span>
        </button>
      </div>
    </div>
  );
}
const MountedReelDrawer = ReelDrawer as React.ComponentType<{
  open: boolean;
  reelId?: string;
  onClose: () => void;
  onSaved?: () => void;
}>;
export function LookSurfaces({ looks, reload }: { looks: Look[]; reload: () => void }) {
  const [params, setParams] = useSearchParams();
  const [families, setFamilies] = useState<Family[]>([]),
    [creating, setCreating] = useState<Look | null>(null),
    [initialTab, setInitialTab] = useState("design");
  useEffect(() => {
    void onair.families().then(setFamilies);
  }, []);
  const lookId = params.get("look");
  const close = () => {
    setCreating(null);
    const next = new URLSearchParams(params);
    next.delete("look");
    next.delete("reel");
    setParams(next);
  };
  const choose = (l: Look, tab = "design") => {
    setInitialTab(tab);
    setCreating({
      ...structuredClone(l),
      id: "",
      name: t("Untitled look"),
      pack: undefined,
      channelIds: [],
    });
  };
  const selected = creating || looks.find((l) => l.id === lookId);
  return (
    <>
      {lookId === "new" && !creating ? (
        <Drawer open size="lg" title={t("New look")} onClose={close}>
          <div className="space-y-6">
            <button
              className="flex w-full items-center gap-4 rounded-xl border border-border bg-primary-subtle p-5 text-left"
              onClick={() => families[0] && choose(families[0].look, "describe")}
            >
              <Sparkles className="text-primary" size={24} />
              <span className="text-base font-semibold">{t("Describe it")}</span>
            </button>
            <section className="space-y-3">
              <h3 className="text-sm font-semibold">{t("Start from a family")}</h3>
              <div className="grid grid-cols-2 gap-3">
                {families.map((f) => (
                  <button
                    key={f.id}
                    className="overflow-hidden rounded-lg border border-border text-left hover:border-primary"
                    onClick={() => choose(f.look)}
                  >
                    <LookTile look={f.look} />
                    <span className="block p-3 text-sm font-medium">{t(f.name)}</span>
                  </button>
                ))}
              </div>
            </section>
            <section className="space-y-3">
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Copy size={15} />
                {t("Duplicate a look")}
              </h3>
              <div className="flex flex-wrap gap-2">
                {looks.map((l) => (
                  <Button key={l.id} variant="outline" size="sm" onClick={() => choose(l)}>
                    {l.name}
                  </Button>
                ))}
              </div>
            </section>
          </div>
        </Drawer>
      ) : null}
      {selected ? (
        <LookDrawer
          key={selected.id || "new"}
          look={selected}
          initialTab={initialTab}
          onClose={close}
          onSaved={(l) => {
            reload();
            if (l) {
              setCreating(null);
              setParams(new URLSearchParams({ look: l.id }));
            }
          }}
        />
      ) : null}
      {params.has("reel") ? (
        <MountedReelDrawer
          open
          reelId={params.get("reel") || "new"}
          onClose={close}
          onSaved={reload}
        />
      ) : null}
    </>
  );
}
