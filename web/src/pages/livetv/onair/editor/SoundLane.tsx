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
}: {
  sound: SceneSound;
  duration: number;
  onChange(s: SceneSound): void;
  onRemove(): void;
  muted: boolean;
  onMute(): void;
  onSolo(): void;
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
  return (
    <section className="rounded border p-2 space-y-2">
      <svg viewBox="0 0 1000 40" className="w-full h-10" aria-label={t("Sound waveform")}>
        {peaks.map((p, i) => (
          <path
            key={i}
            stroke="currentColor"
            d={`M${(i * 1000) / peaks.length} ${20 - p * 20}v${p * 40}`}
          />
        ))}
      </svg>
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
        <Button onClick={onSolo}>{t("Solo")}</Button>
        <Button onClick={onRemove}>{t("Remove sound")}</Button>
      </div>
    </section>
  );
}
