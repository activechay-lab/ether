import { t } from "../../../i18n";
import type { Programme } from "../model";
import type { ChannelLook } from "./model";
/** Draft-only ribbon: personal picks remain stable as their presentation settings change. */
export function rundownPreview(programmes: Programme[], look?: ChannelLook): Programme[] {
  return programmes
    .filter((p) => !p.slate)
    .flatMap((p, i) => {
      const end = Date.parse(p.start) + p.item.durationMs;
      const moments = [
        ...(i === 0 && look?.ident ? [{ kind: "ident", title: t("Ident") }] : []),
        ...(look?.upNext ? [{ kind: "upnext", title: t("Up next") }] : []),
        ...(look?.breaks
          ? [
              { kind: "break", title: t("Break") },
              { kind: "back", title: t("Back from break") },
            ]
          : []),
      ];
      let at = end - moments.length * 12000;
      return [
        { ...p, end: new Date(at).toISOString() },
        ...moments.map((moment) => {
          const start = at;
          at += 12000;
          return {
            id: `${p.id}-${moment.kind}`,
            channelId: p.channelId,
            start: new Date(start).toISOString(),
            end: new Date(at).toISOString(),
            item: { key: 0, title: moment.title, durationMs: 12000 },
            slate: { kind: moment.kind },
          };
        }),
      ];
    });
}
