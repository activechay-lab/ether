import { useEffect, useRef, useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { ColorPicker } from "../../../../components/ColorPicker";
import { t } from "../../../../i18n";
import {
  studio,
  type Style,
  type StyleScene,
  type Brand,
  type LibraryAsset,
  type StyleControl,
} from "../studio-model";
import {
  styleBindings as engineBindings,
  renderDocument,
  releaseRenderer,
  loadSceneAssets,
  loadStyleFonts,
  type SceneBindings,
} from "../scene-renderer";
import { onair, type Bindings } from "../model";
import { DescribeLook } from "../DescribeLook";
import { LibraryPicker } from "../../library/LibraryPicker";

export function styleBindings(style: Style, slots: Record<string, string> = {}): SceneBindings {
  const bindings = engineBindings(style);
  return { ...bindings, slots: { ...bindings.slots, ...slots } };
}
export function StyleThumbnail({
  style,
  scene = style.scenes.find((x) => x.role === "ident"),
  slots = {},
  playing = false,
  time,
}: {
  style: Style;
  scene?: StyleScene;
  slots?: Record<string, string>;
  playing?: boolean;
  time?: number;
}) {
  const ref = useRef<HTMLCanvasElement>(null),
    [hover, setHover] = useState(false);
  const [loaded, setLoaded] = useState<SceneBindings | null>(null);
  const bindingKey = JSON.stringify(styleBindings(style, slots));
  const documentKey = JSON.stringify(scene?.document);
  useEffect(() => {
    let live = true;
    setLoaded(null);
    if (scene)
      void Promise.all([
        loadStyleFonts(style),
        loadSceneAssets(scene.document, JSON.parse(bindingKey) as SceneBindings),
      ])
        .then(([, b]) => {
          if (live) setLoaded(b);
        })
        .catch(() => {});
    return () => {
      live = false;
    };
  }, [bindingKey, documentKey]);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    return () => {
      if (ctx) releaseRenderer(ctx);
    };
  }, []);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx || !scene) return;
    let frame = 0;
    const start = performance.now();
    const bindings = loaded ?? (JSON.parse(bindingKey) as SceneBindings);
    const draw = () =>
      renderDocument(
        ctx,
        scene.document,
        bindings,
        time ??
          (playing || hover
            ? ((performance.now() - start) / 1000) % scene.document.duration
            : scene.document.poster),
      );
    const animate = () => {
      draw();
      frame = requestAnimationFrame(animate);
    };
    draw();
    if (time === undefined && (playing || hover)) frame = requestAnimationFrame(animate);
    return () => cancelAnimationFrame(frame);
  }, [loaded, bindingKey, documentKey, playing, hover, time]);

  return (
    <canvas
      ref={ref}
      width={640}
      height={360}
      className="aspect-video w-full rounded-lg bg-black"
      aria-label={t("{name} preview", { name: scene?.name ?? style.name })}
      tabIndex={0}
      onFocus={() => setHover(!matchMedia("(prefers-reduced-motion: reduce)").matches)}
      onBlur={() => setHover(false)}
      onMouseEnter={() => setHover(!matchMedia("(prefers-reduced-motion: reduce)").matches)}
      onMouseLeave={() => setHover(false)}
    />
  );
}
const fonts = [
  "inter",
  "playfairdisplay",
  "specialelite",
  "fraunces",
  "bungee",
  "fredoka",
  "orbitron",
  "vt323",
  "spacegrotesk",
  "ibmplexmono",
  "archivoblack",
  "notosans",
];
function luminance(hex: string) {
  const c = hex
    .replace("#", "")
    .match(/../g)
    ?.map((x) => parseInt(x, 16) / 255)
    .map((x) => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4)) ?? [0, 0, 0];
  return c[0]! * 0.2126 + c[1]! * 0.7152 + c[2]! * 0.0722;
}
function harmony(hex: string, angle: number) {
  const rgb = hex.match(/[a-f\d]{2}/gi)?.map((x) => parseInt(x, 16) / 255) ?? [0, 0, 0];
  const max = Math.max(...rgb),
    min = Math.min(...rgb),
    d = max - min;
  let h =
    d === 0
      ? 0
      : max === rgb[0]!
        ? ((rgb[1]! - rgb[2]!) / d) % 6
        : max === rgb[1]!
          ? (rgb[2]! - rgb[0]!) / d + 2
          : (rgb[0]! - rgb[1]!) / d + 4;
  h = ((h * 60 + angle + 360) % 360) / 60;
  const x = d * (1 - Math.abs((h % 2) - 1)),
    m = min;
  const c =
    h < 1
      ? [d, x, 0]
      : h < 2
        ? [x, d, 0]
        : h < 3
          ? [0, d, x]
          : h < 4
            ? [0, x, d]
            : h < 5
              ? [x, 0, d]
              : [d, 0, x];
  return (
    "#" +
    c
      .map((v) =>
        Math.round((v + m) * 255)
          .toString(16)
          .padStart(2, "0"),
      )
      .join("")
  );
}
export function RemixPanel(p: {
  style: Style;
  onChange(style: Style): void;
  onOpenScene?(sceneId: string): void;
}) {
  const s = p.style,
    [picker, setPicker] = useState(false),
    [assets, setAssets] = useState<LibraryAsset[]>([]),
    [live, setLive] = useState<Bindings>({});
  useEffect(() => {
    void onair.bindings({ channel: s.channelIds[0] }).then(setLive);
  }, [s.channelIds[0]]);
  useEffect(() => {
    void studio
      .library()
      .then(setAssets)
      .catch(() => {});
  }, []);
  const brand = (patch: Partial<Brand>) =>
    p.onChange({
      ...s,
      brand: { ...s.brand, ...patch },
      ...(patch.textures
        ? {
            scenes: s.scenes.map((scene) => ({
              ...scene,
              document: {
                ...scene.document,
                post: patch.textures!.map(
                  (type) =>
                    scene.document.post?.find((effect) => effect.type === type) ?? {
                      type,
                      amount: `{textures.${type}}`,
                    },
                ),
              },
            })),
          }
        : {}),
    });
  const contrast =
    (Math.max(luminance(s.brand.palette.text), luminance(s.brand.palette.background)) + 0.05) /
    (Math.min(luminance(s.brand.palette.text), luminance(s.brand.palette.background)) + 0.05);
  return (
    <div className="space-y-6">
      <fieldset className="space-y-3">
        <legend className="font-semibold">{t("Brand kit")}</legend>
        <label className="block text-sm">
          {t("Monogram")}
          <Input
            value={s.brand.logo?.length === 64 ? "" : (s.brand.logo ?? "")}
            maxLength={3}
            onChange={(e) => brand({ logo: e.target.value })}
          />
        </label>
        <Button variant="outline" onClick={() => setPicker(true)}>
          {t("Choose logo from Library")}
        </Button>
        <LibraryPicker
          open={picker}
          kinds={["image"]}
          onPick={(a) => {
            brand({ logo: a.digest });
            setPicker(false);
          }}
          onClose={() => setPicker(false)}
        />
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          {(Object.keys(s.brand.palette) as (keyof Brand["palette"])[]).map((key) => (
            <ColorPicker
              key={key}
              label={t(
                { background: "Background", accent: "Accent", text: "Text", glow: "Glow" }[key],
              )}
              value={s.brand.palette[key]}
              onChange={(v) => brand({ palette: { ...s.brand.palette, [key]: v } })}
            />
          ))}
        </div>
        <p className="text-xs" role="status">
          {t("Text contrast: {ratio}:1", { ratio: contrast.toFixed(2) })}{" "}
          {contrast >= 4.5 ? t("Good readability") : t("Increase text contrast")}
        </p>
        <div className="flex flex-wrap gap-2">
          {[
            { label: t("Complementary"), angles: [180] },
            { label: t("Analogous"), angles: [-30, 30] },
            { label: t("Triad"), angles: [120, 240] },
          ].map((h) => (
            <Button
              key={h.label}
              variant="outline"
              onClick={() =>
                brand({
                  palette: {
                    ...s.brand.palette,
                    glow: harmony(s.brand.palette.accent, h.angles[0]!),
                    ...(h.angles.length > 1
                      ? { background: harmony(s.brand.palette.accent, h.angles[1]!) }
                      : {}),
                  },
                })
              }
            >
              {h.label}
              <span
                className="h-4 w-4 rounded-full"
                style={{ background: harmony(s.brand.palette.accent, h.angles[0]!) }}
              />
            </Button>
          ))}
        </div>
        {(["display", "body"] as const).map((key) => (
          <label key={key} className="block text-sm">
            {key === "display" ? t("Display font") : t("Body font")}
            <select
              className="block w-full rounded border border-border bg-surface p-2"
              value={s.brand.type[key]}
              onChange={(e) => brand({ type: { ...s.brand.type, [key]: e.target.value } })}
            >
              {[...fonts, ...assets.filter((a) => a.kind === "font").map((a) => a.digest)].map(
                (f) => (
                  <option key={f} value={f}>
                    {assets.find((a) => a.digest === f)?.name ?? f}
                  </option>
                ),
              )}
            </select>
            <span
              className="block py-2 text-2xl"
              style={{ fontFamily: `onair-${s.brand.type[key]}` }}
            >
              {t("Your next favourite channel")}
            </span>
          </label>
        ))}
        <label className="block text-sm">
          {t("Tagline")}
          <Input value={s.brand.tagline} onChange={(e) => brand({ tagline: e.target.value })} />
        </label>
        <label className="block text-sm">
          {t("Energy: Calm to lively")}
          <input
            className="w-full accent-primary"
            type="range"
            min={0}
            max={1}
            step={0.01}
            value={s.brand.energy}
            onChange={(e) => brand({ energy: +e.target.value })}
          />
        </label>
        <div className="flex flex-wrap gap-2">
          {["grain", "flicker", "scanlines", "vignette"].map((x) => (
            <Button
              key={x}
              variant={s.brand.textures.includes(x) ? "secondary" : "outline"}
              aria-pressed={s.brand.textures.includes(x)}
              onClick={() =>
                brand({
                  textures: s.brand.textures.includes(x)
                    ? s.brand.textures.filter((v) => v !== x)
                    : [...s.brand.textures, x],
                })
              }
            >
              {t(x)}
            </Button>
          ))}
        </div>
      </fieldset>
      {Array.from(new Set(s.controls.map((c) => c.group ?? t("Controls")))).map((group) => (
        <fieldset key={group} className="space-y-3">
          <legend className="font-semibold">{t(group)}</legend>
          {s.controls
            .filter((c) => (c.group ?? t("Controls")) === group)
            .map((c) => (
              <div key={c.id}>
                <RemixControl
                  control={c}
                  assets={assets}
                  onChange={(value) =>
                    p.onChange({
                      ...s,
                      controls: s.controls.map((x) => (x.id === c.id ? { ...x, value } : x)),
                    })
                  }
                />
              </div>
            ))}
        </fieldset>
      ))}
      <DescribeLook look={s} onChange={p.onChange} />
      <div className="grid grid-cols-2 gap-3">
        {s.scenes.map((scene) => (
          <button
            key={scene.id}
            className="rounded-lg border border-border p-1 text-left"
            onClick={() => p.onOpenScene?.(scene.id)}
          >
            <StyleThumbnail
              style={s}
              scene={scene}
              slots={{ ...engineBindings(s, live).slots, "next.poster": live.next?.backdrop ?? "" }}
            />
            <span className="text-xs">{scene.name}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

function RemixControl({
  control: c,
  assets,
  onChange,
}: {
  control: StyleControl;
  assets: LibraryAsset[];
  onChange(v: string | number | boolean): void;
}) {
  const [open, setOpen] = useState(false);
  if (c.kind === "color")
    return <ColorPicker label={t(c.label)} value={String(c.value)} onChange={onChange} />;
  if (c.kind === "image")
    return (
      <div className="space-y-2">
        <p className="text-sm">{t(c.label)}</p>
        {c.value ? (
          <img
            className="h-20 object-contain"
            alt={t(c.label)}
            src={studio.thumb(String(c.value))}
          />
        ) : null}
        <Button variant="outline" onClick={() => setOpen(true)}>
          {t("Choose image")}
        </Button>
        <LibraryPicker
          open={open}
          kinds={["image"]}
          onClose={() => setOpen(false)}
          onPick={(a) => {
            onChange(a.digest);
            setOpen(false);
          }}
        />
      </div>
    );
  if (c.kind === "font")
    return (
      <label className="block text-sm">
        {t(c.label)}
        <select
          className="block w-full rounded border border-border bg-surface p-2"
          value={String(c.value)}
          onChange={(e) => onChange(e.target.value)}
        >
          {[...fonts, ...assets.filter((a) => a.kind === "font").map((a) => a.digest)].map((f) => (
            <option key={f} value={f}>
              {assets.find((a) => a.digest === f)?.name ?? f}
            </option>
          ))}
        </select>
      </label>
    );
  return (
    <label className="block text-sm">
      {t(c.label)}
      <Input
        type={c.kind === "toggle" ? "checkbox" : c.kind === "number" ? "number" : "text"}
        checked={c.kind === "toggle" ? Boolean(c.value) : undefined}
        value={c.kind === "toggle" ? undefined : String(c.value)}
        min={c.min}
        max={c.max}
        step={c.step}
        onChange={(e) =>
          onChange(
            c.kind === "toggle"
              ? e.target.checked
              : c.kind === "number"
                ? +e.target.value
                : e.target.value,
          )
        }
      />
    </label>
  );
}
