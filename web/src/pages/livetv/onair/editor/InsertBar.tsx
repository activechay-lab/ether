import { useState } from "react";
import { Button, Select } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import { LibraryPicker } from "../../library/LibraryPicker";
import type { StyleScene, AssetKind } from "../studio-model";
import type { Layer } from "../scene-renderer";
import { insertLayer, newLayerId } from "./scene-edit";
export function InsertBar(p: {
  scene: StyleScene;
  onChange(scene: StyleScene): void;
  onSelect(ids: string[]): void;
  onPen?(): void;
}) {
  const [pick, setPick] = useState<AssetKind>();
  const add = (type: string, props: Partial<Layer> = {}) => {
    const id = newLayerId(p.scene.document, type);
    p.onChange({
      ...p.scene,
      document: insertLayer(p.scene.document, {
        id,
        type,
        name: t(type),
        x: 760,
        y: 440,
        w: 400,
        h: 200,
        fill: "{accent}",
        ...props,
      }),
    });
    p.onSelect([id]);
  };
  const shape = (kind: string) => {
    const path =
      kind === "ellipse"
        ? "M0 100 A200 100 0 1 0 400 100 A200 100 0 1 0 0 100 Z"
        : kind === "line"
          ? "M0 0 L400 200"
          : kind === "star"
            ? Array.from({ length: 10 }, (_, i) => {
                const a = (i * Math.PI) / 5 - Math.PI / 2,
                  r = i % 2 ? 80 : 180;
                return `${i ? "L" : "M"}${200 + Math.cos(a) * r} ${200 + Math.sin(a) * r}`;
              }).join(" ") + " Z"
            : kind === "polygon"
              ? "M200 0 L400 100 L300 200 L100 200 L0 100 Z"
              : "M0 0 H400 V200 H0 Z";
    add("path", { name: t(kind), path, stroke: "{text}", strokeWidth: kind === "line" ? 4 : 0 });
  };
  return (
    <div className="flex flex-wrap gap-2" aria-label={t("Insert layer")}>
      <Button
        size="sm"
        variant="secondary"
        onClick={() =>
          add("text", {
            text: t("Your title"),
            font: "{type.display}",
            size: 96,
            fill: "{text}",
            w: undefined,
            h: undefined,
          })
        }
      >
        {t("Text")}
      </Button>
      <Button size="sm" variant="ghost" onClick={p.onPen}>
        {t("Pen")}
      </Button>
      <div className="w-32">
        <Select aria-label={t("Shape")} value="" onChange={(e) => shape(e.target.value)}>
          <option value="" disabled>
            {t("Shape")}
          </option>
          {["rect", "ellipse", "line", "star", "polygon"].map((s) => (
            <option key={s} value={s}>
              {t(s)}
            </option>
          ))}
        </Select>
      </div>
      <Button size="sm" variant="ghost" onClick={() => setPick("image")}>
        {t("Image")}
      </Button>
      <Button size="sm" variant="ghost" onClick={() => setPick("video")}>
        {t("Video")}
      </Button>
      <div className="w-32">
        <Select
          aria-label={t("Live data")}
          value=""
          onChange={(e) => {
            const slot = e.target.value;
            add(slot.includes("poster") || slot.includes("backdrop") ? "image" : "text", {
              text: `{${slot}}`,
              src: `{${slot}}`,
              font: "{type.body}",
              size: 60,
              fill: "{text}",
            });
          }}
        >
          <option value="" disabled>
            {t("Live data")}
          </option>
          {[
            "channel.name",
            "next.title",
            "next.time",
            "next.poster",
            "next.backdrop",
            "now.title",
            "viewer.name",
            "clock",
          ].map((s) => (
            <option key={s} value={s}>
              {t(s)}
            </option>
          ))}
        </Select>
      </div>
      <div className="w-32">
        <Select
          aria-label={t("Effects")}
          value=""
          onChange={(e) => {
            const kind = e.target.value;
            add(kind === "gradient" ? "rect" : kind, {
              w: 1920,
              h: 1080,
              x: 0,
              y: 0,
              fill:
                kind === "gradient"
                  ? {
                      linear: [0, 0, 1920, 1080],
                      stops: [
                        [0, "{bg}"],
                        [1, "{accent}"],
                      ],
                    }
                  : "{accent}",
              stroke: kind === "grid" ? "{accent}" : undefined,
              strokeWidth: kind === "grid" ? 2 : undefined,
              count: kind === "particles" ? 80 : undefined,
              opacity: kind === "noise" ? 0.1 : 1,
            });
          }}
        >
          <option value="" disabled>
            {t("Effects")}
          </option>
          {["gradient", "grid", "particles", "noise"].map((s) => (
            <option key={s} value={s}>
              {t(s)}
            </option>
          ))}
        </Select>
      </div>
      <LibraryPicker
        open={!!pick}
        kinds={pick ? [pick] : []}
        onClose={() => setPick(undefined)}
        onPick={(asset) => {
          if (asset.kind === "video")
            p.onChange({
              ...p.scene,
              video: { assetDigest: asset.digest, mode: "background", fit: "cover", start: 0 },
            });
          else add("image", { src: asset.digest });
          setPick(undefined);
        }}
      />
    </div>
  );
}
