import { Badge, cn } from "@braedonsaunders/appkit-ui";
import { CalendarRange, KeyRound, ListVideo } from "lucide-react";
import { t, tn } from "../../i18n";
import { relativeTime } from "../../lib/format";
import { dayClock, duration, isChannelSource, type Source } from "./model";

export type Selection =
  | { kind: "guide" }
  | { kind: "channel"; id: string }
  | { kind: "new" }
  | { kind: "source"; id: string }
  | { kind: "map"; id: string };

/** Every IPTV and guide source, on the left panel's Sources tab. */
export function SourceList({
  sources,
  selected,
  onSelect,
  query = "",
}: {
  sources: Source[];
  selected: Selection;
  onSelect: (s: Selection) => void;
  /** The Live TV search: narrows the sources by name or by one of their groups. */
  query?: string;
}) {
  const needle = query.trim().toLowerCase();
  const channelSources = sources
    .filter(isChannelSource)
    .filter(
      (s) =>
        !needle ||
        s.name.toLowerCase().includes(needle) ||
        (s.groups ?? []).some((g) => g.name.toLowerCase().includes(needle)),
    );
  return (
    <div className="space-y-4 p-3 sm:p-4">
      <div className="space-y-1.5">
        <h2 className="px-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          {t("Channel sources")}
        </h2>
        {channelSources.length === 0 ? (
          <p className="px-1 text-xs text-fg-muted">
            {t(
              "No channel sources yet. Add an M3U playlist or an add-on for IPTV channels. Guides live on the Guide tab.",
            )}
          </p>
        ) : null}
        {channelSources.map((s) => {
          const on = selected.kind === "source" && selected.id === s.id;
          const epgOnly =
            s.hasPlaylist === false ||
            (!s.playlistUrl && !s.channelCount && (s.guideChannelCount ?? 0) > 0);
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => onSelect({ kind: "source", id: s.id })}
              aria-current={on ? "true" : undefined}
              className={cn(
                "flex w-full items-start gap-3 rounded-xl border bg-surface p-3 text-left transition-colors hover:border-border-strong",
                on ? "border-primary/40 ring-1 ring-primary/30" : "border-border",
              )}
            >
              <span
                className={cn(
                  "grid size-9 shrink-0 place-items-center rounded-lg",
                  epgOnly ? "bg-info/15 text-info" : "bg-primary-subtle text-primary",
                )}
              >
                {epgOnly ? <CalendarRange size={17} /> : <ListVideo size={17} />}
              </span>
              <span className="min-w-0 flex-1 space-y-1">
                <span className="flex items-center gap-2">
                  <span className="truncate text-sm font-medium text-fg">{s.name}</span>
                  {s.disabled ? <Badge>{t("Disabled")}</Badge> : null}
                  {s.auth?.type ? (
                    <KeyRound
                      size={12}
                      className="shrink-0 text-fg-subtle"
                      aria-label={s.auth.type === "xtream" ? t("Xtream login") : t("Signs in")}
                    />
                  ) : null}
                  {s.lastError ? (
                    <Badge variant="destructive" className="shrink-0">
                      {t("Refresh failed")}
                    </Badge>
                  ) : null}
                </span>
                <span className="flex flex-wrap gap-x-2 gap-y-0.5 text-xs text-fg-muted">
                  <span>
                    {epgOnly
                      ? t("Guide only")
                      : tn(s.channelCount ?? 0, "{count} channel", "{count} channels")}
                  </span>
                  {s.guideChannelCount ? (
                    <span>
                      · {tn(s.guideChannelCount, "{count} guide channel", "{count} guide channels")}
                    </span>
                  ) : null}
                  {s.guideUntil ? (
                    <span>· {t("through {time}", { time: dayClock(s.guideUntil) })}</span>
                  ) : null}
                </span>
                <span className="block text-[11px] text-fg-subtle">
                  {s.guideFrom && s.guideUntil
                    ? t("Refreshed {when} · {span} of listings", {
                        when: relativeTime(s.updatedAt),
                        span: duration(Date.parse(s.guideUntil) - Date.parse(s.guideFrom)),
                      })
                    : t("Refreshed {when}", { when: relativeTime(s.updatedAt) })}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
