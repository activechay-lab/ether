import { useEffect, useState } from "react";
import { Button, Input, Select, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import { Upload, FileType, Image, AudioLines, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { t, tn } from "../../../i18n";
import { onair, type OnAirAsset, type Reel, type Look } from "./model";
// Import and pod slots accept W16 components without adding a top-level tab.
export function AssetsPanel({
  importSlot,
  podsSlot,
}: {
  importSlot?: ReactNode;
  podsSlot?: ReactNode;
}) {
  const [params, setParams] = useSearchParams();
  const [looks, setLooks] = useState<Look[]>([]);
  const [reels, setReels] = useState<Reel[]>([]);
  const [kind, setKind] = useState("");
  const [query, setQuery] = useState("");

  const [assets, setAssets] = useState<OnAirAsset[]>([]);
  const [pending, setPending] = useState<
    { id: string; file: File; license: string; busy: boolean }[]
  >([]);
  useEffect(() => {
    void onair.assets().then(setAssets);
    void onair.reels().then(setReels);
    void onair.looks().then(setLooks);
  }, [params.get("reel"), params.get("look")]);
  const upload = async (files: FileList | null, kind: string) => {
    if (!files) return;
    try {
      for (const file of Array.from(files)) {
        if (kind === "font") {
          setPending((s) => [...s, { id: crypto.randomUUID(), file, license: "", busy: false }]);
          continue;
        }
        const a = await onair.uploadAsset(file, kind);
        setAssets((s) => [...s, a]);
      }
    } catch {
      toast.error(t("Upload failed"));
    }
  };
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap gap-2">
        <Input
          aria-label={t("Search assets")}
          placeholder={t("Search assets")}
          value={query}
          onChange={(e) => setQuery(e.currentTarget.value)}
        />
        <Select
          aria-label={t("Asset kind")}
          value={kind}
          onChange={(e) => setKind(e.currentTarget.value)}
        >
          {[
            ["", t("All kinds")],
            ["image", t("Images")],
            ["font", t("Fonts")],
            ["audio", t("Audio")],
            ["clips", t("Clips")],
            ["collections", t("Collections")],
          ].map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </Select>
        {importSlot}
      </div>
      {!kind || kind === "collections" || kind === "clips" ? (
        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">{t("Clip collections")}</h3>
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                setParams((p) => {
                  p.set("reel", "new");
                  return p;
                })
              }
            >
              {t("New collection")}
            </Button>
          </div>
          {reels
            .filter((r) => r.name.toLowerCase().includes(query.toLowerCase()))
            .map((r) => (
              <button
                key={r.id}
                className="flex w-full items-center justify-between rounded-lg border border-border p-3 text-left"
                onClick={() =>
                  setParams((p) => {
                    p.set("reel", r.id);
                    return p;
                  })
                }
              >
                <span className="text-sm font-medium">{r.name}</span>
                <span className="text-xs text-fg-muted">
                  {tn(r.clips.length, "{count} clip", "{count} clips")}
                </span>
              </button>
            ))}
        </section>
      ) : null}
      {podsSlot}

      <div className="grid grid-cols-2 gap-2">
        {[
          { kind: "font", label: t("Fonts"), Icon: FileType, accept: ".ttf,.otf" },
          { kind: "image", label: t("Images"), Icon: Image, accept: "image/*" },
          { kind: "audio", label: t("Audio"), Icon: AudioLines, accept: "audio/*" },
        ].map(({ kind, label, Icon, accept }) => (
          <label
            key={kind}
            className="focus-within:ring-2 focus-within:ring-primary flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed border-border bg-bg-subtle p-4 text-sm text-fg-muted hover:border-primary hover:text-primary"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              void upload(e.dataTransfer.files, kind);
            }}
          >
            <Icon size={22} />
            {label}
            <Upload size={12} />
            <input
              className="sr-only"
              type="file"
              aria-label={label}
              multiple
              accept={accept}
              onChange={(e) => void upload(e.target.files, kind)}
            />
          </label>
        ))}
      </div>
      <div className="space-y-3">
        {pending.map((p) => (
          <div key={p.id} className="space-y-2 rounded-lg border border-border bg-surface p-3">
            <p className="truncate text-sm font-medium">{p.file.name}</p>
            <Select
              aria-label={t("Font licence")}
              value={p.license}
              disabled={p.busy}
              onChange={(e) =>
                setPending((s) =>
                  s.map((x) => (x.id === p.id ? { ...x, license: e.target.value } : x)),
                )
              }
            >
              <option value="">{t("Choose a licence")}</option>
              {["OFL", "Apache", "Owned by me", "Other"].map((s) => (
                <option key={s} value={s}>
                  {t(s)}
                </option>
              ))}
            </Select>
            <Button
              size="sm"
              disabled={!p.license || p.busy}
              onClick={async () => {
                setPending((s) => s.map((x) => (x.id === p.id ? { ...x, busy: true } : x)));
                try {
                  const a = await onair.uploadAsset(p.file, "font", p.license);
                  setAssets((s) => [...s, a]);
                  setPending((s) => s.filter((x) => x.id !== p.id));
                } catch {
                  toast.error(t("Upload failed"));
                  setPending((s) => s.map((x) => (x.id === p.id ? { ...x, busy: false } : x)));
                }
              }}
            >
              {t("Upload")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={p.busy}
              onClick={() => setPending((s) => s.filter((x) => x.id !== p.id))}
            >
              {t("Cancel")}
            </Button>
          </div>
        ))}
        {assets
          .filter(
            (a) => (!kind || a.kind === kind) && a.name.toLowerCase().includes(query.toLowerCase()),
          )
          .map((a) => (
            <div key={a.digest} className="rounded-lg border border-border bg-surface p-3">
              <div className="flex items-center gap-3">
                {a.kind === "audio" ? (
                  <AudioLines size={24} className="text-primary" />
                ) : (
                  <FileType size={24} className="text-fg-muted" />
                )}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{a.name}</p>
                  <p className="text-xs text-fg-muted">
                    {t("{size} KB", { size: Math.round(a.size / 1024) })}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Remove asset")}
                  disabled={a.usedBy.length > 0}
                  onClick={async () => {
                    if (
                      await confirmDialog({
                        title: t("Remove {name}?", { name: a.name }),
                        message: a.usedBy.length
                          ? t("Used by: {layers}", {
                              layers: a.usedBy
                                .map(
                                  (id) =>
                                    looks.find((l) => l.id === id)?.name ??
                                    reels.find((r) => r.id === id)?.name ??
                                    id,
                                )
                                .join(", "),
                            })
                          : t("This asset will be removed from the library."),
                        confirmLabel: t("Remove"),
                        tone: "danger",
                      })
                    ) {
                      await onair.deleteAsset(a.digest);
                      setAssets((s) => s.filter((x) => x.digest !== a.digest));
                    }
                  }}
                >
                  <Trash2 size={13} />
                </Button>
              </div>
              <p className="mt-2 text-xs text-fg-subtle">
                {a.usedBy.length
                  ? t("Used by {layers}", {
                      layers: a.usedBy
                        .map(
                          (id) =>
                            looks.find((l) => l.id === id)?.name ??
                            reels.find((r) => r.id === id)?.name ??
                            id,
                        )
                        .join(", "),
                    })
                  : t("Not used yet")}
              </p>
              {a.kind === "font" && a.license ? (
                <p className="mt-2 text-xs text-fg-subtle">
                  {t("Font licence")}: {a.license}
                </p>
              ) : null}
            </div>
          ))}
      </div>
    </div>
  );
}
