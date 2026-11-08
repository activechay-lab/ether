import { Badge, Button, cn, toast } from "@braedonsaunders/appkit-ui";
import { useEffect } from "react";
import { Clock, Copy, Play, Settings, Sparkles, Star, X } from "lucide-react";
import { Artwork } from "../../components/Artwork";
import { t } from "../../i18n";
import { unit } from "../../lib/format";
import { ChannelLogo } from "./ChannelLogo";
import {
  MINUTE,
  clock,
  dayClock,
  progress,
  type Channel,
  type ChannelSummary,
  type Programme,
} from "./model";

export function ProgrammeInspector({
  channel,
  programme,
  now,
  onClose,
  onPlayChannel,
  onOpenChannelSettings,
  onToggleFavorite,
}: {
  channel: Channel | ChannelSummary;
  programme: Programme;
  now: number;
  onClose: () => void;
  onPlayChannel?: (channel: Channel) => void;
  onOpenChannelSettings?: (channel: Channel | ChannelSummary) => void;
  onToggleFavorite?: (channel: Channel | ChannelSummary) => void;
}) {
  const p = programme;
  const item = p.item;
  const startMs = Date.parse(p.start);
  const endMs = Date.parse(p.end);
  const isLive = startMs <= now && endMs > now;
  const isFuture = startMs > now;
  const durationMinutes = Math.max(1, Math.round((endMs - startMs) / MINUTE));
  const remainingMinutes = Math.max(1, Math.round((endMs - now) / MINUTE));
  const startsInMinutes = Math.max(1, Math.round((startMs - now) / MINUTE));

  const hasEp =
    item.season !== undefined &&
    item.episode !== undefined &&
    (item.season > 0 || item.episode > 0);
  const epTag = hasEp
    ? `S${String(item.season).padStart(2, "0")}E${String(item.episode).padStart(2, "0")}`
    : undefined;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const hasStream = "streamUrl" in channel && Boolean(channel.streamUrl);
  const copyChannelStream = async () => {
    if (!channel.streamUrl) return;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(channel.streamUrl);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = channel.streamUrl;
        textarea.style.position = "fixed";
        textarea.style.opacity = "0";
        document.body.appendChild(textarea);
        textarea.focus();
        textarea.select();
        const ok = document.execCommand("copy");
        document.body.removeChild(textarea);
        if (!ok) throw new Error("copy failed");
      }
      toast.success(t("Stream link copied"));
    } catch {
      toast.error(t("Failed to copy link"));
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={item.title}
      className="fixed inset-0 z-[120] flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs animate-in fade-in"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="relative flex max-h-[90vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-border bg-surface shadow-2xl">
        {/* Top Header */}
        <div className="flex shrink-0 items-center justify-between border-b border-border bg-bg-subtle px-4 py-3">
          <div className="flex min-w-0 items-center gap-2.5">
            <ChannelLogo name={channel.name} logo={channel.logo} seed={channel.id} size={30} />
            <div className="min-w-0">
              <div className="flex items-center gap-1.5">
                <span className="font-mono text-xs tabular-nums text-fg-subtle">
                  {channel.number}
                </span>
                <span className="truncate text-sm font-semibold text-fg">{channel.name}</span>
                {channel.favorite && (
                  <Star size={12} className="shrink-0 fill-warning text-warning" />
                )}
              </div>
              <span className="block truncate text-[11px] text-fg-muted">
                {channel.group || (channel.kind === "virtual" ? t("Virtual") : t("Live TV"))}
              </span>
            </div>
          </div>
          <div className="flex items-center gap-1">
            {onToggleFavorite && (
              <Button
                variant="ghost"
                size="sm"
                aria-label={channel.favorite ? t("Unfavourite") : t("Favourite")}
                onClick={() => onToggleFavorite(channel)}
              >
                <Star size={14} className={cn(channel.favorite && "fill-warning text-warning")} />
              </Button>
            )}
            <Button variant="ghost" size="sm" aria-label={t("Close")} onClick={onClose}>
              <X size={16} />
            </Button>
          </div>
        </div>

        {/* Scrollable Body */}
        <div className="app-scroll flex-1 overflow-y-auto">
          {/* Banner / Poster */}
          {item.thumb || item.art ? (
            <div className="relative h-44 w-full overflow-hidden bg-bg">
              <Artwork
                src={item.art || item.thumb}
                alt=""
                title=""
                className="size-full object-cover"
              />
              <div className="absolute inset-0 bg-gradient-to-t from-surface via-surface/30 to-transparent" />
            </div>
          ) : null}

          <div className="space-y-4 p-5">
            {/* Title & Episode */}
            <div className="space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                {isLive ? (
                  <Badge
                    variant="destructive"
                    className="animate-pulse gap-1 font-bold tracking-wider"
                  >
                    <span className="size-1.5 rounded-full bg-white" />
                    {t("LIVE NOW")}
                  </Badge>
                ) : isFuture ? (
                  <Badge variant="secondary">{t("UPCOMING")}</Badge>
                ) : (
                  <Badge variant="secondary" className="opacity-70">
                    {t("ENDED")}
                  </Badge>
                )}
                {epTag ? <Badge variant="outline">{epTag}</Badge> : null}
                {item.rating ? <Badge variant="secondary">{item.rating}</Badge> : null}
                {item.premiere ? (
                  <Badge variant="info" className="gap-1">
                    <Sparkles size={11} /> {t("Premiere")}
                  </Badge>
                ) : null}
              </div>

              <h2 className="text-xl font-bold tracking-tight text-fg">
                {item.title || t("Untitled broadcast")}
              </h2>

              {item.episodeTitle && item.episodeTitle !== item.title ? (
                <p className="text-sm font-medium text-fg-muted">"{item.episodeTitle}"</p>
              ) : null}
            </div>

            {/* Time schedule & countdown */}
            <div className="rounded-xl border border-border/70 bg-bg-subtle/60 p-3.5 space-y-2">
              <div className="flex items-center justify-between text-xs">
                <span className="flex items-center gap-1.5 font-medium text-fg">
                  <Clock size={14} className="text-primary" />
                  {clock(p.start)} – {clock(p.end)}
                  <span className="text-fg-subtle font-normal">
                    ({unit(durationMinutes, "minute")})
                  </span>
                </span>
                <span className="text-fg-muted">
                  {isLive
                    ? t("{time} remaining", { time: unit(remainingMinutes, "minute") })
                    : isFuture
                      ? startsInMinutes > 60
                        ? t("Starts in {time}", {
                            time: unit(Math.round(startsInMinutes / 60), "hour"),
                          })
                        : t("Starts in {time}", { time: unit(startsInMinutes, "minute") })
                      : dayClock(p.start)}
                </span>
              </div>

              {isLive ? (
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
                  <div
                    className="h-full rounded-full bg-primary transition-all duration-500"
                    style={{ width: `${Math.min(100, Math.max(0, progress(p, now) * 100))}%` }}
                  />
                </div>
              ) : null}
            </div>

            {/* Genres */}
            {item.genres && item.genres.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {item.genres.map((g) => (
                  <span
                    key={g}
                    className="rounded-md bg-surface-hover px-2 py-0.5 text-xs font-medium text-fg-muted"
                  >
                    {g}
                  </span>
                ))}
              </div>
            ) : null}

            {/* Synopsis */}
            <div className="space-y-1.5">
              <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
                {t("Synopsis")}
              </h3>
              <p className="text-sm leading-relaxed text-fg-muted whitespace-pre-line">
                {item.summary || t("No synopsis available for this programme.")}
              </p>
            </div>
          </div>
        </div>

        {/* Footer Actions */}
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-border bg-bg-subtle p-3.5">
          <div className="flex gap-2">
            {onOpenChannelSettings ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  onClose();
                  onOpenChannelSettings(channel);
                }}
              >
                <Settings size={14} /> {t("Channel settings")}
              </Button>
            ) : null}
            {hasStream ? (
              <Button variant="ghost" size="sm" onClick={copyChannelStream}>
                <Copy size={13} /> {t("Copy link")}
              </Button>
            ) : null}
          </div>

          {onPlayChannel ? (
            <Button
              size="sm"
              onClick={() => {
                onClose();
                onPlayChannel(channel as Channel);
              }}
            >
              <Play size={14} className="fill-current" /> {t("Watch now")}
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
