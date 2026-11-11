import { t } from "../../../i18n";
import type { Programme } from "../model";
import type { ChannelLook } from "./model";

export type RundownMoment = "tonight" | "upnext" | "ident" | "break" | "back";

export const RUNDOWN_MOMENTS: RundownMoment[] = ["tonight", "upnext", "ident", "break", "back"];

export function isRundownMoment(v: string): v is RundownMoment {
  return (RUNDOWN_MOMENTS as string[]).includes(v);
}

export function rundownMomentLabel(kind: RundownMoment): string {
  switch (kind) {
    case "tonight":
      return t("Upcoming shows");
    case "upnext":
      return t("Up next");
    case "ident":
      return t("Station ID");
    case "break":
      return t("Break");
    case "back":
      return t("Back from break");
  }
}

function normalizeMoments(list?: string[]): RundownMoment[] {
  if (!list) return [];
  const seen = new Set<RundownMoment>();
  const out: RundownMoment[] = [];
  for (const raw of list) {
    const v = raw.trim().toLowerCase();
    if (isRundownMoment(v) && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

export function effectiveBeforeBreak(look?: ChannelLook): RundownMoment[] {
  if (look?.beforeBreak !== undefined) return normalizeMoments(look.beforeBreak);
  const out: RundownMoment[] = [];
  const tonightOn = look?.tonight ?? !!look?.upNext;
  if (tonightOn) out.push("tonight");
  if (look?.upNext) out.push("upnext");
  if (!look?.breakPattern || look.breakPattern.bumperStyle !== "none") out.push("break");
  return out;
}

export function effectiveAfterBreak(look?: ChannelLook): RundownMoment[] {
  if (look?.afterBreak !== undefined) return normalizeMoments(look.afterBreak);
  const out: RundownMoment[] = [];
  if (!look?.breakPattern || look.breakPattern.bumperStyle !== "none") out.push("back");
  if (look?.breakPattern?.stationId) out.push("ident");
  return out;
}

export function effectiveBetweenProgrammes(look?: ChannelLook): RundownMoment[] {
  if (look?.betweenProgrammes !== undefined) return normalizeMoments(look.betweenProgrammes);
  const out: RundownMoment[] = [];
  const tonightOn = look?.tonight ?? !!look?.upNext;
  if (tonightOn) out.push("tonight");
  if (look?.upNext) out.push("upnext");
  if (look?.breakPattern?.stationId) out.push("ident");
  return out;
}

/** Draft-only ribbon: personal picks remain stable as their presentation settings change. */
export function rundownPreview(programmes: Programme[], look?: ChannelLook): Programme[] {
  return programmes
    .filter((p) => !p.slate)
    .flatMap((p, i) => {
      const end = Date.parse(p.start) + p.item.durationMs;
      const tailKinds: RundownMoment[] = look?.breaks
        ? [...effectiveBeforeBreak(look), ...effectiveAfterBreak(look)]
        : effectiveBetweenProgrammes(look);
      const moments = [
        ...(i === 0 && look?.ident ? [{ kind: "ident" as const, title: t("Ident") }] : []),
        ...tailKinds.map((kind) => ({ kind, title: rundownMomentLabel(kind) })),
      ];
      let at = Math.max(Date.parse(p.start) + 4000, end - moments.length * 8000);
      return [
        { ...p, end: new Date(at).toISOString() },
        ...moments.map((moment, idx) => {
          const start = at;
          at += 8000;
          return {
            id: `${p.id}-${moment.kind}-${idx}`,
            channelId: p.channelId,
            start: new Date(start).toISOString(),
            end: new Date(at).toISOString(),
            item: { key: 0, title: moment.title, durationMs: 8000 },
            slate: { kind: moment.kind },
          };
        }),
      ];
    });
}
