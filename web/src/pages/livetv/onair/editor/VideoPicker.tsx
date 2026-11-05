import { useEffect, useState } from "react";
import { Button, Select, Switch, Input } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import { studio, type LibraryAsset, type StyleScene } from "../studio-model";
export function VideoPicker({
  scene,
  onChange,
}: {
  scene: StyleScene;
  onChange(scene: StyleScene): void;
}) {
  const [assets, setAssets] = useState<LibraryAsset[]>([]);
  useEffect(() => {
    void studio.library().then((a) => setAssets(a.filter((a) => a.kind === "video")));
  }, []);
  const video = scene.video;
  const patch = (p: Partial<NonNullable<StyleScene["video"]>>) => {
    if (video) onChange({ ...scene, video: { ...video, ...p } });
  };
  return (
    <section className="space-y-3">
      <label>
        {t("Scene video")}
        <Select
          value={video?.assetDigest ?? ""}
          onChange={(e) =>
            onChange({
              ...scene,
              video: e.target.value
                ? { assetDigest: e.target.value, mode: "background", fit: "cover", start: 0 }
                : undefined,
            })
          }
        >
          <option value="">{t("No video")}</option>
          {assets.map((a) => (
            <option key={a.digest} value={a.digest}>
              {a.name}
            </option>
          ))}
        </Select>
      </label>
      {video && (
        <>
          <Select
            aria-label={t("Video mode")}
            value={video.mode}
            onChange={(e) => patch({ mode: e.target.value as "background" | "replace" })}
          >
            <option value="background">{t("Background")}</option>
            <option value="replace">{t("Replace scene")}</option>
          </Select>
          <Select
            aria-label={t("Video fit")}
            value={video.fit}
            onChange={(e) => patch({ fit: e.target.value as "cover" | "contain" })}
          >
            <option value="cover">{t("Cover")}</option>
            <option value="contain">{t("Contain")}</option>
          </Select>
          <div className="flex overflow-hidden h-16">
            {Array.from({ length: 6 }, (_, i) => (
              <img
                key={i}
                alt=""
                className="w-1/6 object-cover"
                src={studio.thumb(video.assetDigest)}
              />
            ))}
          </div>
          <label>
            {t("Start offset")}
            <Input
              type="range"
              min={0}
              max={(assets.find((a) => a.digest === video.assetDigest)?.durationMs ?? 30000) / 1000}
              step={0.01}
              value={video.start}
              onChange={(e) => patch({ start: Number(e.target.value) })}
            />
          </label>
          <label>
            {t("Crossfade")}
            <Switch
              checked={!!video.crossfade}
              onChange={(e) => patch({ crossfade: e.target.checked })}
            />
          </label>
          <Button onClick={() => onChange({ ...scene, video: undefined })}>
            {t("Remove video")}
          </Button>
        </>
      )}
    </section>
  );
}
