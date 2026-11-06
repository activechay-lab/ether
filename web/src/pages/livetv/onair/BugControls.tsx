import { studio } from "./studio-model";
import type { Brand } from "./studio-model";
import { useMediaURL } from "./useMediaURL";
import { Switch } from "@braedonsaunders/appkit-ui";
import { Field, Segmented } from "../../../components/fields";
import { t } from "../../../i18n";
import type { BugSettings } from "./model";

import { resolveBug } from "./bug";
export { resolveBug } from "./bug";
export function BugControls({
  value,
  brand,
  onChange,
}: {
  value: BugSettings;
  brand?: Brand;
  onChange: (v: BugSettings) => void;
}) {
  const b = resolveBug(value);
  const logo = useMediaURL(brand?.logo && brand.logo.length > 3 ? studio.media(brand.logo) : "");
  const set = (patch: BugSettings) => onChange({ ...value, ...patch });
  return (
    <div className="grid gap-6 sm:grid-cols-2">
      <div className="space-y-4">
        <div
          className="relative aspect-video overflow-hidden rounded-lg border border-border bg-gradient-to-br from-slate-800 to-slate-950"
          aria-label={t("Corner logo")}
        >
          <span
            className="absolute p-4"
            style={{
              top: b.position.startsWith("top") ? 0 : undefined,
              bottom: b.position.startsWith("bottom") ? 0 : undefined,
              left: b.position.endsWith("Left") ? 0 : undefined,
              right: b.position.endsWith("Right") ? 0 : undefined,
              opacity: b.opacity,
              filter: b.style === "monochrome" ? "grayscale(1) brightness(2)" : undefined,
              fontSize: b.size === "small" ? 12 : b.size === "large" ? 24 : 18,
            }}
          >
            {b.source === "logo" && logo ? (
              <img src={logo} className="h-[2em] max-w-24 object-contain" alt={t("Logo")} />
            ) : (
              <span className="font-bold text-white">
                {b.source === "wordmark"
                  ? brand?.tagline || brand?.logo?.slice(0, 3) || t("Wordmark")
                  : brand?.logo?.slice(0, 3) || t("Logo")}
              </span>
            )}
          </span>
        </div>
        <Field label={t("Position")}>
          <div className="grid grid-cols-2 gap-2" role="group" aria-label={t("Position")}>
            {(
              [
                { value: "topLeft", label: t("Top left") },
                { value: "topRight", label: t("Top right") },
                { value: "bottomLeft", label: t("Bottom left") },
                { value: "bottomRight", label: t("Bottom right") },
              ] as const
            ).map((o) => (
              <button
                key={o.value}
                type="button"
                aria-pressed={b.position === o.value}
                onClick={() => set({ position: o.value })}
                className={`rounded-md border px-3 py-2 text-sm ${b.position === o.value ? "border-primary bg-primary/10 text-primary" : "border-border text-fg-muted"}`}
              >
                {o.label}
              </button>
            ))}
          </div>
        </Field>
        <Field label={t("Opacity")}>
          <div className="flex items-center gap-3">
            <input
              className="w-full accent-primary"
              aria-label={t("Opacity")}
              type="range"
              min="15"
              max="60"
              step="1"
              value={Math.round(b.opacity * 100)}
              onChange={(e) => set({ opacity: Number(e.target.value) / 100 })}
            />
            <output className="shrink-0 whitespace-nowrap text-sm tabular-nums">
              {t("{n}%", { n: Math.round(b.opacity * 100) })}
            </output>
          </div>
        </Field>
      </div>
      <div className="space-y-4">
        <Field label={t("Style")}>
          <Segmented
            ariaLabel={t("Style")}
            value={b.style}
            onChange={(style) => set({ style: style as BugSettings["style"] })}
            options={[
              { value: "monochrome", label: t("Monochrome") },
              { value: "original", label: t("Original colour") },
            ]}
          />
        </Field>
        <Field label={t("Size")}>
          <Segmented
            ariaLabel={t("Size")}
            value={b.size}
            onChange={(size) => set({ size: size as BugSettings["size"] })}
            options={[
              { value: "small", label: t("Small") },
              { value: "medium", label: t("Medium") },
              { value: "large", label: t("Large") },
            ]}
          />
        </Field>
        <Field label={t("Artwork")}>
          <Segmented
            ariaLabel={t("Artwork")}
            value={b.source}
            onChange={(source) => set({ source: source as BugSettings["source"] })}
            options={[
              { value: "logo", label: t("Logo") },
              { value: "wordmark", label: t("Wordmark") },
            ]}
          />
        </Field>
        <label className="flex items-center justify-between gap-3 text-sm">
          {t("Fade during breaks")}
          <Switch
            aria-label={t("Fade during breaks")}
            checked={b.fadeBreaks}
            onChange={(e) => set({ fadeBreaks: e.currentTarget.checked })}
          />
        </label>
        <label className="flex items-center justify-between gap-3 text-sm">
          {t("Fade during credits")}
          <Switch
            aria-label={t("Fade during credits")}
            checked={b.fadeCredits}
            onChange={(e) => set({ fadeCredits: e.currentTarget.checked })}
          />
        </label>
      </div>
    </div>
  );
}
