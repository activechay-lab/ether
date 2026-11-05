import { useState } from "react";
import { Button, Input, Popover } from "@braedonsaunders/appkit-ui";
import { Pipette } from "lucide-react";
import { t } from "../i18n";
function hsv(h: number, s: number, v: number) {
  const f = (n: number) => {
    const k = (n + h / 60) % 6;
    return Math.round((v - v * s * Math.max(0, Math.min(k, 4 - k, 1))) * 255)
      .toString(16)
      .padStart(2, "0");
  };
  return "#" + f(5) + f(3) + f(1);
}
/** AppKit-style reusable colour control, ready to upstream. */
export function ColorPicker({
  value,
  onChange,
  label,
  swatches = [],
}: {
  value: string;
  onChange: (v: string) => void;
  label: string;
  swatches?: string[];
}) {
  const [open, setOpen] = useState(false);
  const [placement, setPlacement] = useState<{ align: "start" | "end"; side: "top" | "bottom" }>({
    align: "start",
    side: "bottom",
  });
  const [hue, setHue] = useState(270);
  const [hex, setHex] = useState(value);
  const change = (v: string) => {
    setHex(v);
    onChange(v);
  };
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      align={placement.align}
      side={placement.side}
      className="max-h-[calc(100dvh-2rem)] w-64 max-w-[calc(100vw-1rem)] space-y-3 overflow-y-auto p-3"
      trigger={
        <button
          className="flex flex-1 flex-col items-center gap-2 rounded-lg border border-border bg-surface p-2 hover:bg-surface-hover"
          onClick={(e) => {
            const rect = e.currentTarget.getBoundingClientRect();
            const below = window.innerHeight - rect.bottom;
            setPlacement({
              align: rect.left + 256 > window.innerWidth - 8 ? "end" : "start",
              side: below < 400 && rect.top > below ? "top" : "bottom",
            });
            setOpen(!open);
          }}
          aria-label={label}
        >
          <span
            className="h-9 w-full rounded-md border border-border"
            style={{ background: value }}
          />
          <span className="text-xs text-fg-muted">{label}</span>
        </button>
      }
    >
      <div
        className="h-36 touch-none rounded-md"
        role="slider"
        tabIndex={0}
        aria-label={t("Saturation and brightness")}
        aria-valuetext={value}
        style={{
          background: `linear-gradient(to top,#000,transparent),linear-gradient(to right,#fff,hsl(${hue},100%,50%))`,
        }}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          const r = e.currentTarget.getBoundingClientRect();
          change(
            hsv(
              hue,
              Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)),
              1 - Math.max(0, Math.min(1, (e.clientY - r.top) / r.height)),
            ),
          );
        }}
        onPointerMove={(e) => {
          if (!e.buttons) return;
          const r = e.currentTarget.getBoundingClientRect();
          change(
            hsv(
              hue,
              Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)),
              1 - Math.max(0, Math.min(1, (e.clientY - r.top) / r.height)),
            ),
          );
        }}
      />
      <input
        type="range"
        min="0"
        max="360"
        value={hue}
        aria-label={t("Hue")}
        className="w-full"
        style={{ accentColor: `hsl(${hue},100%,50%)` }}
        onChange={(e) => {
          setHue(+e.target.value);
          change(hsv(+e.target.value, 0.7, 0.9));
        }}
      />
      <div className="flex gap-2">
        <Input
          aria-label={t("Hex colour")}
          value={hex}
          onChange={(e) => {
            setHex(e.target.value);
            if (/^#[a-f\d]{6}$/i.test(e.target.value)) onChange(e.target.value);
          }}
        />
        {"EyeDropper" in window ? (
          <Button
            variant="outline"
            size="sm"
            aria-label={t("Pick from screen")}
            onClick={async () => {
              try {
                const browser = window as unknown as {
                  EyeDropper: new () => { open(): Promise<{ sRGBHex: string }> };
                };
                const result = await new browser.EyeDropper().open();
                change(result.sRGBHex);
              } catch {
                /* cancelled */
              }
            }}
          >
            <Pipette size={16} />
          </Button>
        ) : null}
      </div>
      <div className="flex gap-2">
        {swatches.map((s, i) => (
          <button
            key={i}
            className="size-7 rounded-full border border-border"
            style={{ background: s }}
            aria-label={s}
            onClick={() => change(s)}
          />
        ))}
      </div>
    </Popover>
  );
}
