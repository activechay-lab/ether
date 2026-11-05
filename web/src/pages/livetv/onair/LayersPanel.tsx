import type { Look } from "./model";
import { useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Eye, EyeOff, Lock, Unlock, Diamond, Link2 } from "lucide-react";
import { Field } from "../../../components/fields";
import { t } from "../../../i18n";
export function LayersPanel({
  selected,
  onSelect,
  look,
  onChange,
}: {
  look: Look;
  onChange: (l: Look) => void;
  selected: string;
  onSelect: (s: string) => void;
}) {
  const [hidden, setHidden] = useState<string[]>([]),
    [locked, setLocked] = useState<string[]>([]),
    [keyframes, setKeyframes] = useState(0);
  const layer = look.layers?.[selected] || {};
  const patch = (p: NonNullable<Look["layers"]>[string]) =>
    onChange({ ...look, layers: { ...look.layers, [selected]: { ...layer, ...p } } });
  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">{t("Layer · {name}", { name: t(selected) })}</h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setKeyframes((n) => n + 1)}
          aria-label={t("Add keyframe")}
        >
          <Diamond size={14} />
        </Button>
      </div>
      <Field label={t("Text")}>
        <Input
          value={layer.text ?? "{next.title}"}
          onChange={(e) => patch({ text: e.target.value })}
          key={selected}
        />
        <Button
          size="sm"
          variant="ghost"
          onClick={(e) => {
            const input = e.currentTarget.previousElementSibling as HTMLInputElement;
            patch({ text: input.value.startsWith("{") ? t("Your title") : "{next.title}" });
          }}
        >
          <Link2 size={12} />
          {t("next.title")}
        </Button>
      </Field>
      <Field label={t("Fit mode")}>
        <select
          value={layer.fit || "Shrink"}
          onChange={(e) => patch({ fit: e.target.value })}
          className="w-full rounded-md border border-border bg-surface p-2 text-sm"
        >
          {["Shrink", "Wrap", "Clip"].map((x) => (
            <option key={x}>{t(x)}</option>
          ))}
        </select>
      </Field>
      <div className="grid grid-cols-2 gap-3">
        {["Size", "Opacity", "Position X", "Position Y"].map((x) => (
          <Field label={t(x)} key={x}>
            <div className="flex">
              <Input
                type="number"
                key={selected + x}
                value={
                  layer[
                    (
                      {
                        Size: "size",
                        Opacity: "opacity",
                        "Position X": "x",
                        "Position Y": "y",
                      } as const
                    )[x as "Size"]
                  ] ?? (x === "Opacity" ? 100 : x === "Size" ? 128 : 0)
                }
                onChange={(e) =>
                  patch({
                    [(
                      {
                        Size: "size",
                        Opacity: "opacity",
                        "Position X": "x",
                        "Position Y": "y",
                      } as const
                    )[x as "Size"]]: +e.target.value,
                  })
                }
              />
              <Button
                variant="ghost"
                size="sm"
                aria-label={t("Add keyframe")}
                onClick={() => setKeyframes((n) => n + 1)}
              >
                <Diamond size={12} />
              </Button>
            </div>
          </Field>
        ))}
      </div>
      <Field label={t("Behaviour")}>
        <select
          value={layer.behaviour || "Typewriter"}
          onChange={(e) => patch({ behaviour: e.target.value })}
          className="w-full rounded-md border border-border bg-surface p-2 text-sm"
        >
          {[
            "Typewriter",
            "Flicker",
            "Wiggle",
            "Bounce",
            "Glint",
            "Chroma split",
            "Drift",
            "Pop",
          ].map((x) => (
            <option key={x}>{t(x)}</option>
          ))}
        </select>
      </Field>
      <p className="text-xs text-fg-muted">{t("{count} keyframes added", { count: keyframes })}</p>
      <div className="space-y-1 border-t border-border pt-4">
        {["Title", "Tagline", "Underline", "Moon", "Atmosphere", "Particles"].map((name) => (
          <div
            key={name}
            className={`flex items-center rounded-md p-2 ${selected === name ? "bg-primary-subtle text-primary" : "text-fg-muted"}`}
          >
            <button className="flex-1 text-left text-sm" onClick={() => onSelect(name)}>
              {t(name)}
            </button>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("Toggle visibility")}
              onClick={() =>
                setHidden((h) => (h.includes(name) ? h.filter((x) => x !== name) : [...h, name]))
              }
            >
              {hidden.includes(name) ? <EyeOff size={13} /> : <Eye size={13} />}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("Toggle lock")}
              onClick={() =>
                setLocked((h) => (h.includes(name) ? h.filter((x) => x !== name) : [...h, name]))
              }
            >
              {locked.includes(name) ? <Lock size={13} /> : <Unlock size={13} />}
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}
export function Timeline({
  selected,
  onSelect,
}: {
  selected: string;
  onSelect: (s: string) => void;
}) {
  return (
    <div className="relative mt-5 overflow-hidden rounded-lg border border-border bg-surface p-3">
      <div className="mb-3 flex justify-between text-xs text-fg-subtle">
        {[0, 2, 4, 6, 8].map((n) => (
          <span key={n}>{n}s</span>
        ))}
      </div>
      <div className="pointer-events-none absolute inset-y-2 left-[43%] z-10 w-px bg-primary" />
      {["Title", "Tagline", "Underline", "Moon", "Atmosphere", "Particles"].map((name, i) => (
        <button
          key={name}
          onClick={() => onSelect(name)}
          className={`flex h-8 w-full items-center gap-3 border-t border-border text-left text-xs ${selected === name ? "text-primary" : "text-fg-muted"}`}
        >
          <span className="w-24 shrink-0">{t(name)}</span>
          <span className="flex flex-1 items-center gap-12">
            <span className="text-primary">◆</span>
            <span className="h-px flex-1 bg-border" />
            <span className="text-primary" style={{ marginRight: `${i * 7}%` }}>
              ◆
            </span>
          </span>
        </button>
      ))}
    </div>
  );
}
