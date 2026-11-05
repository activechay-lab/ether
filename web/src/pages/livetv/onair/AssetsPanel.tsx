import { useEffect, useState } from "react";
import { Button, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import { Upload, FileType, Image, AudioLines, Film, Trash2 } from "lucide-react";
import { t } from "../../../i18n";
import { onair, type OnAirAsset } from "./model";
export function AssetsPanel() {
  const [assets, setAssets] = useState<OnAirAsset[]>([]);
  useEffect(() => {
    void onair.assets().then(setAssets);
  }, []);
  const upload = async (files: FileList | null, kind: string) => {
    if (!files) return;
    try {
      for (const file of Array.from(files)) {
        const a = await onair.uploadAsset(file, kind);
        setAssets((s) => [...s, a]);
      }
    } catch {
      toast.error(t("Upload failed"));
    }
  };
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-2">
        {[
          { kind: "font", label: t("Fonts"), Icon: FileType, accept: ".ttf,.otf,.woff2" },
          { kind: "image", label: t("Images"), Icon: Image, accept: "image/*" },
          { kind: "audio", label: t("Audio"), Icon: AudioLines, accept: "audio/*" },
          { kind: "video", label: t("Video loops"), Icon: Film, accept: "video/*" },
        ].map(({ kind, label, Icon, accept }) => (
          <label
            key={kind}
            className="flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed border-border bg-bg-subtle p-4 text-sm text-fg-muted hover:border-primary hover:text-primary"
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
              className="hidden"
              type="file"
              multiple
              accept={accept}
              onChange={(e) => void upload(e.target.files, kind)}
            />
          </label>
        ))}
      </div>
      <div className="space-y-3">
        {assets.map((a) => (
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
                onClick={async () => {
                  if (
                    await confirmDialog({
                      title: t("Remove {name}?", { name: a.name }),
                      message: a.usedBy.length
                        ? t("Used by: {layers}", { layers: a.usedBy.join(", ") })
                        : t("This asset will be removed from the look."),
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
                ? t("Used by {layers}", { layers: a.usedBy.join(", ") })
                : t("Not used yet")}
            </p>
            {a.kind === "font" ? (
              <select
                className="mt-2 w-full rounded-md border border-border bg-bg p-2 text-xs"
                aria-label={t("Font licence")}
              >
                {["OFL", "Apache", "Owned by me", "Other"].map((s) => (
                  <option key={s}>{t(s)}</option>
                ))}
              </select>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}
