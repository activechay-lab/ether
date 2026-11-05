import type { Look, Moment } from "./model";
import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { Eye, EyeOff } from "lucide-react";
import { Field } from "../../../components/fields";
import { t } from "../../../i18n";
import { documentFor, type Layer } from "./scene-renderer";
export function sceneLayers(look: Look, moment: Moment): Layer[] {
  const out: Layer[] = [];
  const walk = (layers: Layer[]) => {
    for (const layer of layers) {
      out.push(layer);
      if (layer.layers) walk(layer.layers);
    }
  };
  walk(documentFor(look.family, moment)?.layers ?? []);
  return out;
}
export function LayersPanel({
  selected,
  onSelect,
  look,
  onChange,
  moment,
}: {
  look: Look;
  onChange: (l: Look) => void;
  selected: string;
  onSelect: (s: string) => void;
  moment: Moment;
}) {
  const layers = sceneLayers(look, moment);
  const source = layers.find((l) => l.id === selected);
  const layer = look.layers?.[selected] ?? {};
  const patch = (p: NonNullable<Look["layers"]>[string]) =>
    onChange({ ...look, layers: { ...look.layers, [selected]: { ...layer, ...p } } });
  return (
    <div className="space-y-5">
      <h3 className="text-sm font-semibold">{t("Layers")}</h3>
      <div className="space-y-1">
        {layers.map((l) => (
          <div key={l.id} className="flex items-center gap-2 rounded-lg border border-border p-2">
            <button
              aria-pressed={l.id === selected}
              className="min-w-0 flex-1 truncate text-left text-sm"
              onClick={() => onSelect(l.id)}
            >
              {l.id}
            </button>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("Toggle visibility for {name}", { name: l.id })}
              aria-pressed={!look.layers?.[l.id]?.hidden}
              onClick={() =>
                onChange({
                  ...look,
                  layers: {
                    ...look.layers,
                    [l.id]: { ...look.layers?.[l.id], hidden: !look.layers?.[l.id]?.hidden },
                  },
                })
              }
            >
              {look.layers?.[l.id]?.hidden ? <EyeOff size={14} /> : <Eye size={14} />}
            </Button>
          </div>
        ))}
      </div>
      {source ? (
        <>
          <h4 className="text-sm font-medium">{t("Layer · {name}", { name: selected })}</h4>
          {source.type === "text" ? (
            <>
              <Field label={t("Text")}>
                <Input
                  value={layer.text ?? (typeof source.text === "string" ? source.text : "")}
                  onChange={(e) => patch({ text: e.currentTarget.value })}
                />
              </Field>
              <Field label={t("Fit mode")}>
                <Select
                  value={layer.fit ?? source.fit ?? "shrink"}
                  onChange={(e) => patch({ fit: e.currentTarget.value })}
                >
                  {["shrink", "wrap", "clip"].map((v) => (
                    <option key={v} value={v}>
                      {t({ shrink: "Shrink", wrap: "Wrap", clip: "Clip" }[v]!)}
                    </option>
                  ))}
                </Select>
              </Field>
            </>
          ) : null}
          <div className="grid grid-cols-2 gap-3">
            {(["x", "y", "opacity", ...(source.type === "text" ? ["size"] : [])] as const).map(
              (key) => (
                <Field
                  key={key}
                  label={t(
                    { x: "Position X", y: "Position Y", opacity: "Opacity", size: "Size" }[
                      key as "x"
                    ],
                  )}
                >
                  <Input
                    type="number"
                    aria-label={t(
                      { x: "Position X", y: "Position Y", opacity: "Opacity", size: "Size" }[
                        key as "x"
                      ],
                    )}
                    min={key === "opacity" ? 0 : undefined}
                    max={key === "opacity" ? 100 : undefined}
                    value={
                      layer[key as "x"] ??
                      (key === "opacity"
                        ? 100
                        : typeof source[key as "x"] === "number"
                          ? (source[key as "x"] as number)
                          : 0)
                    }
                    onChange={(e) => patch({ [key]: Number(e.currentTarget.value) })}
                  />
                </Field>
              ),
            )}
          </div>
        </>
      ) : (
        <p className="text-sm text-fg-muted">{t("Select a layer to edit it.")}</p>
      )}
    </div>
  );
}
export function Timeline({
  look,
  moment,
  selected,
  onSelect,
}: {
  look: Look;
  moment: Moment;
  selected: string;
  onSelect: (id: string) => void;
}) {
  return (
    <div className="mt-5 flex flex-wrap gap-2">
      {sceneLayers(look, moment).map((l) => (
        <Button
          key={l.id}
          variant={selected === l.id ? "secondary" : "ghost"}
          size="sm"
          onClick={() => onSelect(l.id)}
        >
          {l.id}
        </Button>
      ))}
    </div>
  );
}
