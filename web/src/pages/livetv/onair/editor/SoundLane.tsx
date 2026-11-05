import { useEffect, useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import { studio, type SceneSound } from "../studio-model";
export function SoundLane({
  sound,
  duration,
  onChange,
  onRemove,
  muted,
  onMute,
  onSolo,
  solo,
}: {
  sound: SceneSound;
  duration: number;
  onChange(s: SceneSound): void;
  onRemove(): void;
  muted: boolean;
  onMute(): void;
  onSolo(): void;
  solo: boolean;
}) {
  const [peaks, setPeaks] = useState<number[]>([]),
    [length, setLength] = useState(duration);
  useEffect(() => {
    void studio
      .waveform(sound.assetDigest)
      .then((w) => {
        setPeaks(w.peaks);
        setLength(w.durationMs / 1000);
      })
      .catch(() => setPeaks([]));
  }, [sound.assetDigest]);
  const drag = (
    e: React.PointerEvent,
    initial: number,
    field: "at" | "start" | "end" | "fadeIn" | "fadeOut",
  ) => {
    e.stopPropagation();
    const node = e.currentTarget,
      x = e.clientX,
      width = node.closest("[data-track]")!.getBoundingClientRect().width;
    node.setPointerCapture(e.pointerId);
    const move = (event: PointerEvent) => {
      const value =
        initial + ((event.clientX - x) / width) * duration * (field === "fadeOut" ? -1 : 1);
      onChange({
        ...sound,
        [field]: Math.max(
          0,
          field === "start"
            ? Math.min(value, (sound.end ?? length) - 0.01)
            : field === "end"
              ? Math.min(length, Math.max(value, sound.start + 0.01))
              : field === "at"
                ? Math.min(duration, value)
                : Math.min(value, (sound.end ?? length) - sound.start),
        ),
      });
    };
    const end = () => node.removeEventListener("pointermove", move as EventListener);
    node.addEventListener("pointermove", move as EventListener);
    node.addEventListener("pointerup", end, { once: true });
  };
  return (
    <section className="rounded border p-2 space-y-2">
      <div data-track className="relative h-14 bg-current/5 overflow-hidden">
        <div
          className="absolute h-full bg-purple-500/20"
          style={{
            left: `${(sound.at / duration) * 100}%`,
            width: `${(((sound.end ?? length) - sound.start) / duration) * 100}%`,
          }}
          onPointerDown={(e) => drag(e, sound.at, "at")}
        >
          <svg viewBox="0 0 1000 40" className="w-full h-10" aria-label={t("Sound waveform")}>
            {peaks
              .slice(
                Math.floor((sound.start / length) * peaks.length),
                Math.ceil(((sound.end ?? length) / length) * peaks.length),
              )
              .map((p, i, arr) => (
                <path
                  key={i}
                  stroke="currentColor"
                  d={`M${(i * 1000) / arr.length} ${20 - p * 20}v${p * 40}`}
                />
              ))}
          </svg>
          <button
            className="absolute left-0 top-0 h-full"
            aria-label={t("Trim sound start")}
            onPointerDown={(e) => drag(e, sound.start, "start")}
          >
            ┃
          </button>
          <button
            className="absolute right-0 top-0 h-full"
            aria-label={t("Trim sound end")}
            onPointerDown={(e) => drag(e, sound.end ?? length, "end")}
          >
            ┃
          </button>
          <button
            className="absolute top-0"
            style={{ left: `${(sound.fadeIn / ((sound.end ?? length) - sound.start)) * 100}%` }}
            aria-label={t("Fade in")}
            onPointerDown={(e) => drag(e, sound.fadeIn, "fadeIn")}
          >
            ◆
          </button>
          <button
            className="absolute bottom-0"
            style={{ right: `${(sound.fadeOut / ((sound.end ?? length) - sound.start)) * 100}%` }}
            aria-label={t("Fade out")}
            onPointerDown={(e) => drag(e, sound.fadeOut, "fadeOut")}
          >
            ◆
          </button>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        {(["at", "start", "end", "fadeIn", "fadeOut", "volume"] as const).map((prop) => (
          <label key={prop} className="text-xs">
            {
              {
                at: t("Scene offset"),
                start: t("Trim start"),
                end: t("Trim end"),
                fadeIn: t("Fade in"),
                fadeOut: t("Fade out"),
                volume: t("Volume"),
              }[prop]
            }
            <Input
              className="w-24"
              type="number"
              step={0.05}
              min={0}
              max={prop === "volume" ? 2 : prop === "at" ? duration : length}
              value={sound[prop] ?? length}
              onChange={(e) => {
                const value = Number(e.target.value);
                onChange({ ...sound, [prop]: value });
              }}
            />
          </label>
        ))}
        <Button onClick={onMute} aria-pressed={muted}>
          {t("Mute")}
        </Button>
        <Button onClick={onSolo} aria-pressed={solo}>
          {t("Solo")}
        </Button>
        <Button onClick={onRemove}>{t("Remove sound")}</Button>
      </div>
    </section>
  );
}
