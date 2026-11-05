import { useEffect, useRef, useState } from "react";
import { Volume2, VolumeX } from "lucide-react";
import { cn } from "@braedonsaunders/appkit-ui";
import { t } from "../../../i18n";
import type { Channel } from "../model";
import { InfoTip } from "./RecipeTab";
export type ScreenStyle = "" | "card" | "static" | "bars" | "brb" | "testcard" | "error";

function ScreenCard({
  style,
  name,
  station,
  active,
  onSelect,
}: {
  style: ScreenStyle;
  name: string;
  station: string;
  active: boolean;
  onSelect: () => void;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [hover, setHover] = useState(false);
  const [sound, setSound] = useState(false);
  useEffect(() => {
    if (!sound) return;
    const context = new AudioContext();
    const gain = context.createGain();
    gain.gain.value = 0.025;
    gain.connect(context.destination);
    const oscillator = context.createOscillator();
    oscillator.frequency.value = style === "static" ? 60 : 1000;
    oscillator.connect(gain);
    oscillator.start();
    if (style === "static") {
      const buffer = context.createBuffer(1, context.sampleRate * 2, context.sampleRate);
      const samples = buffer.getChannelData(0);
      for (let i = 0; i < samples.length; i++) samples[i] = Math.random() * 2 - 1;
      const noise = context.createBufferSource();
      noise.buffer = buffer;
      noise.loop = true;
      noise.connect(gain);
      noise.start();
    }
    return () => {
      void context.close();
    };
  }, [sound, style]);
  useEffect(() => {
    const ctx = canvas.current?.getContext("2d");
    if (!ctx) return;
    let frame = 0;
    const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const draw = (time: number) => {
      const w = 320,
        h = 180;
      ctx.fillStyle = "#101827";
      ctx.fillRect(0, 0, w, h);
      if (style === "static") {
        const pixels = ctx.createImageData(w, h);
        for (let i = 0; i < pixels.data.length; i += 4) {
          const v = Math.random() * 200;
          pixels.data[i] = v;
          pixels.data[i + 1] = v;
          pixels.data[i + 2] = v;
          pixels.data[i + 3] = 255;
        }
        ctx.putImageData(pixels, 0, 0);
        ctx.fillStyle = "rgba(255,255,255,.25)";
        ctx.fillRect(0, (time / 25) % h, w, 12);
      } else if (style === "bars" || style === "testcard") {
        ["#d6d6d6", "#c4c400", "#00c4c4", "#00c400", "#c400c4", "#c40000", "#0000c4"].forEach(
          (c, i) => {
            ctx.fillStyle = c;
            ctx.fillRect((i * w) / 7, 0, w / 7 + 1, h * 0.72);
          },
        );
        ["#101010", "#222c80", "#eeeeee", "#603575", "#111111"].forEach((c, i) => {
          ctx.fillStyle = c;
          ctx.fillRect((i * w) / 5, h * 0.72, w / 5 + 1, h * 0.28);
        });
        if (style === "testcard") {
          ctx.strokeStyle = "#fff";
          ctx.lineWidth = 2;
          ctx.beginPath();
          ctx.arc(160, 90, 72, 0, Math.PI * 2);
          ctx.stroke();
          ctx.beginPath();
          ctx.moveTo(160, 0);
          ctx.lineTo(160, 180);
          ctx.moveTo(0, 90);
          ctx.lineTo(320, 90);
          ctx.stroke();
        }
      } else {
        const gradient = ctx.createLinearGradient(0, 0, w, h);
        gradient.addColorStop(0, "#153d43");
        gradient.addColorStop(1, "#15162c");
        ctx.fillStyle = gradient;
        ctx.fillRect(0, 0, w, h);
        ctx.fillStyle = "rgba(255,255,255,.07)";
        ctx.beginPath();
        ctx.arc(270 + Math.sin(time / 700) * 12, 30, 85, 0, Math.PI * 2);
        ctx.fill();
        ctx.fillStyle = "#fff";
        ctx.font = "600 20px sans-serif";
        ctx.textAlign = "center";
        ctx.fillText(
          style === "brb"
            ? t("We'll be right back")
            : style === "error"
              ? t("Can't play this channel")
              : t("Off air"),
          160,
          94,
        );
      }
      if (station) {
        ctx.fillStyle = "rgba(0,0,0,.7)";
        ctx.fillRect(0, 143, w, 37);
        ctx.fillStyle = "#fff";
        ctx.textAlign = "center";
        ctx.font = "12px sans-serif";
        ctx.fillText(station, 160, 166);
      }
      if (hover && !reduced) frame = requestAnimationFrame(draw);
    };
    draw(0);
    return () => cancelAnimationFrame(frame);
  }, [style, station, hover]);
  const audible = style === "static" || style === "bars" || style === "testcard";
  return (
    <div
      className="relative min-w-0"
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => {
        setHover(false);
        setSound(false);
      }}
    >
      <button
        type="button"
        aria-pressed={active}
        onFocus={() => setHover(true)}
        onBlur={() => {
          setHover(false);
          setSound(false);
        }}
        onClick={onSelect}
        className={cn(
          "w-full overflow-hidden rounded-lg border bg-surface text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary",
          active ? "border-primary ring-2 ring-primary" : "border-border",
        )}
        aria-label={name}
      >
        <canvas ref={canvas} width={320} height={180} className="aspect-video w-full" />
        <span className="block truncate px-2 py-2 text-xs font-medium text-fg">{name}</span>
      </button>
      {audible ? (
        <button
          type="button"
          aria-label={sound ? t("Mute {name}", { name }) : t("Listen to {name}", { name })}
          aria-pressed={sound}
          onClick={() => setSound((v) => !v)}
          className="absolute right-1.5 top-1.5 rounded-full bg-black/70 p-1.5 text-white"
        >
          {sound ? <Volume2 size={13} /> : <VolumeX size={13} />}
        </button>
      ) : null}
    </div>
  );
}
export function ScreenPicker({
  label,
  value,
  onChange,
  station = "",
  defaultLabel,
  allowError = false,
}: {
  label: string;
  value: ScreenStyle;
  onChange: (value: ScreenStyle) => void;
  station?: string;
  defaultLabel?: string;
  allowError?: boolean;
}) {
  const options: { value: ScreenStyle; label: string }[] = [
    ...(defaultLabel
      ? [{ value: "" as const, label: t("Default ({style})", { style: defaultLabel }) }]
      : []),
    { value: "card", label: t("Modern card") },
    { value: "static", label: t("Analogue static") },
    { value: "bars", label: t("Colour bars") },
    { value: "brb", label: t("We'll be right back") },
    { value: "testcard", label: t("Test card") },
    ...(allowError ? [{ value: "error" as const, label: t("Show an error") }] : []),
  ];
  return (
    <div className="space-y-3">
      <h4 className="flex items-center gap-2 text-sm font-medium text-fg">
        {label}
        <InfoTip
          text={t(
            "Plays when the channel has no programme or its stream is unavailable. Sound previews are muted until you press the speaker.",
          )}
        />
      </h4>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {options.map((o) => (
          <ScreenCard
            key={o.value}
            style={o.value}
            name={o.label}
            station={station}
            active={o.value === value}
            onSelect={() => onChange(o.value)}
          />
        ))}
      </div>
    </div>
  );
}
export function ChannelScreens({
  channel,
  onChange,
}: {
  channel: Channel;
  onChange: (patch: Partial<Channel>) => void;
}) {
  return (
    <section className="space-y-5">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-muted">
        {t("Screens")}
      </h3>
      <ScreenPicker
        label={t("When a channel is off air")}
        value={channel.screens?.offAir || ""}
        onChange={(offAir) => onChange({ screens: { ...channel.screens, offAir } })}
        station={channel.name}
        defaultLabel={t("Modern card")}
      />
      <ScreenPicker
        label={t("When a channel can't play")}
        value={channel.screens?.noSignal || ""}
        onChange={(noSignal) => onChange({ screens: { ...channel.screens, noSignal } })}
        station={channel.name}
        defaultLabel={t("Modern card")}
      />
    </section>
  );
}
