import { Button, Input, Tooltip, toast } from "@braedonsaunders/appkit-ui";
import { Info, Shuffle, RotateCcw, Play, Upload, PenTool } from "lucide-react";
import { Field, ChipGroup } from "../../../components/fields";
import { ColorPicker } from "../../../components/ColorPicker";
import { t } from "../../../i18n";
import { referenceLooks } from "./fixtures";
import { normalizeFont } from "./scene-renderer";
import { MOMENTS, onair, type Look, type BrandTokens } from "./model";
import { BugControls } from "./BugControls";
export function DesignPanel({ look, onChange }: { look: Look; onChange: (l: Look) => void }) {
  const b = look.brand;
  const set = (p: Partial<BrandTokens>) => onChange({ ...look, brand: { ...b, ...p } });
  const section = (title: string, children: React.ReactNode) => (
    <section className="space-y-3 border-b border-border pb-5">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-muted">{title}</h3>
      {children}
    </section>
  );
  const luminance = (hex: string) => {
    const rgb = hex
      .match(/[a-f\d]{2}/gi)
      ?.map((x) => parseInt(x, 16) / 255)
      .map((x) => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4)) || [0, 0, 0];
    return 0.2126 * rgb[0]! + 0.7152 * rgb[1]! + 0.0722 * rgb[2]!;
  };
  const l1 = luminance(b.palette.background),
    l2 = luminance(b.palette.text),
    contrast = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
  return (
    <div className="space-y-5">
      {section(
        t("Brand"),
        <>
          <Field label={t("Name")}>
            <Input
              value={look.name}
              onChange={(e) => onChange({ ...look, name: e.target.value })}
            />
          </Field>
          <div className="flex gap-2">
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border border-border px-3 py-2 text-sm">
              <Upload size={14} />
              {t("Logo")}
              <input
                className="hidden"
                type="file"
                accept="image/*"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) {
                    void onair
                      .uploadAsset(file, "image")
                      .then((a) => set({ logo: a.digest }))
                      .catch(() => toast.error(t("Upload failed")));
                  }
                }}
              />
            </label>
            <Button variant="outline" onClick={() => set({ logo: look.name.slice(0, 1) })}>
              <PenTool size={14} />
              {t("Draw one")}
            </Button>
            {b.logo ? (
              <span className="text-primary">{b.logo.length < 3 ? b.logo : t("Uploaded")}</span>
            ) : null}
          </div>
          <Field
            label={
              <span className="flex items-center gap-1">
                {t("Tagline")}
                <Tooltip content={t("Appears on idents and off-air cards.")}>
                  <Info size={12} />
                </Tooltip>
              </span>
            }
          >
            <Input value={b.tagline} onChange={(e) => set({ tagline: e.target.value })} />
          </Field>
        </>,
      )}
      {section(
        t("Colour"),
        <>
          <div className="flex gap-2">
            {(Object.keys(b.palette) as (keyof typeof b.palette)[]).map((role) => (
              <ColorPicker
                key={role}
                value={b.palette[role]}
                label={t(
                  { background: "Background", accent: "Accent", text: "Text", glow: "Glow" }[role],
                )}
                swatches={Object.values(b.palette)}
                onChange={(v) => set({ palette: { ...b.palette, [role]: v } })}
              />
            ))}
          </div>
          <div className="flex items-center justify-between">
            <div className="flex gap-1">
              {referenceLooks.map((l) => (
                <button
                  key={l.id}
                  className="size-6 rounded-full border-2 border-border"
                  style={{ background: l.brand.palette.accent }}
                  title={l.name}
                  onClick={() => set({ palette: l.brand.palette })}
                />
              ))}
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                const index = (b.seed + 1) % referenceLooks.length;
                set({ palette: referenceLooks[index]!.brand.palette, seed: b.seed + 1 });
              }}
            >
              <Shuffle size={13} />
              {t("Shuffle")}
            </Button>
          </div>
          <p className="flex items-center gap-2 text-xs text-fg-muted">
            <span className={contrast < 4.5 ? "text-warning" : "text-success"}>●</span>
            {t("Text contrast {ratio}:1", { ratio: contrast.toFixed(1) })}
            <Tooltip content={t("Aim for at least 4.5:1 for readable on-air text.")}>
              <Info size={12} />
            </Tooltip>
          </p>
        </>,
      )}
      {section(
        t("Type"),
        <div className="grid grid-cols-2 gap-2">
          {referenceLooks.map((l) => (
            <button
              key={l.id}
              className={`rounded-lg border p-3 text-left ${b.type.display === l.brand.type.display ? "border-primary bg-primary-subtle" : "border-border bg-surface"}`}
              onClick={() => set({ type: l.brand.type })}
            >
              <span
                className="block text-xl"
                style={{
                  fontFamily: `onair-${normalizeFont(l.brand.type.display)}`,
                  color: b.palette.accent,
                }}
              >
                Aa
              </span>
              <span
                className="block text-xs"
                style={{ fontFamily: `onair-${normalizeFont(l.brand.type.body)}` }}
              >
                {t("Up next")}
              </span>
              <span className="mt-2 block truncate text-xs text-fg-muted">
                {l.brand.type.display}
              </span>
            </button>
          ))}
        </div>,
      )}
      {section(
        t("Motion"),
        <>
          <Field label={t("Energy")}>
            <input
              aria-label={t("Energy")}
              className="w-full accent-primary"
              type="range"
              min="0"
              max="1"
              step=".01"
              value={b.energy}
              onChange={(e) => set({ energy: +e.target.value })}
            />
            <div className="flex justify-between text-xs text-fg-muted">
              <span>{t("Calm")}</span>
              <span>{t("Lively")}</span>
            </div>
          </Field>
          <Field label={t("Texture")}>
            <ChipGroup
              options={["Grain", "Flicker", "Scanlines", "Vignette", "Chroma"].map((x) => ({
                value: x,
                label: t(x),
              }))}
              value={b.textures}
              onChange={(textures) => set({ textures })}
              ariaLabel={t("Texture")}
              size="sm"
            />
          </Field>
        </>,
      )}
      {section(
        t("Sound"),
        <>
          <div className="flex gap-2">
            <select
              className="w-full rounded-md border border-border bg-surface p-2 text-sm"
              aria-label={t("Sting")}
              value={b.sting}
              onChange={(e) => set({ sting: e.target.value })}
            >
              {["Soft chime", "Film projector", "Synth hit", "Bubble pop", "None"].map((s) => (
                <option key={s}>{t(s)}</option>
              ))}
            </select>
            <Button
              variant="outline"
              aria-label={t("Play sting")}
              onClick={() => {
                const a = new AudioContext();
                const o = a.createOscillator(),
                  g = a.createGain();
                o.type = "sine";
                o.frequency.setValueAtTime(660, a.currentTime);
                o.frequency.exponentialRampToValueAtTime(330, a.currentTime + 0.5);
                g.gain.setValueAtTime(b.volume * 0.15, a.currentTime);
                g.gain.exponentialRampToValueAtTime(0.001, a.currentTime + 0.6);
                o.connect(g);
                g.connect(a.destination);
                o.start();
                o.stop(a.currentTime + 0.6);
                o.onended = () => void a.close();
              }}
            >
              <Play size={14} />
            </Button>
          </div>
          <input
            aria-label={t("Volume")}
            type="range"
            className="w-full accent-primary"
            min="0"
            max="1"
            step=".01"
            value={b.volume}
            onChange={(e) => set({ volume: +e.target.value })}
          />
        </>,
      )}
      {section(
        t("Corner logo"),
        <BugControls value={look.bug ?? {}} onChange={(bug) => onChange({ ...look, bug })} />,
      )}
      <details className="space-y-3">
        <summary className="cursor-pointer text-sm text-fg-muted">{t("More options")}</summary>
        <Button variant="outline" onClick={() => set({ seed: b.seed + 1 })}>
          <RotateCcw size={14} />
          {t("New shapes")}
        </Button>
        <Field label={t("Language")}>
          <select
            value={b.language}
            onChange={(e) => set({ language: e.target.value })}
            className="w-full rounded-md border border-border bg-surface p-2"
          >
            <option value="en">{t("English")}</option>
            <option value="fr">{t("French")}</option>
          </select>
        </Field>
        {MOMENTS.map((m) => (
          <Field key={m} label={t(m)}>
            <div className="flex gap-2">
              {[4, 8, 12].map((duration) => (
                <Button
                  key={duration}
                  variant={look.moments[m]?.duration === duration ? "secondary" : "outline"}
                  size="sm"
                  onClick={() =>
                    onChange({
                      ...look,
                      moments: { ...look.moments, [m]: { duration, poster: 3 } },
                    })
                  }
                >
                  {t("{n} s", { n: duration })}
                </Button>
              ))}
            </div>
            <Input
              placeholder={t(
                {
                  ident: "You are watching",
                  tonight: "Tonight",
                  upnext: "Up next",
                  break: "We’ll be right back",
                  back: "Welcome back",
                  offair: "Back soon",
                  welcome: "Welcome back",
                  bug: "",
                  lowerthird: "Now playing",
                }[m],
              )}
              value={b.words[m] || ""}
              onChange={(e) => set({ words: { ...b.words, [m]: e.target.value } })}
            />
          </Field>
        ))}
      </details>
    </div>
  );
}
