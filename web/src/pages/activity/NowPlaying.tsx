import { formatBitrate } from "../../lib/format";
import { Badge, Button, EmptyState, Select, toast } from "@braedonsaunders/appkit-ui";
import {
  ArrowRight,
  Cpu,
  Gauge,
  Globe,
  Home,
  LayoutGrid,
  PlayCircle,
  Radio,
  Rows3,
  Square,
} from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Artwork } from "../../components/Artwork";
import { Meter } from "../../components/charts";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { Kpi, KpiRow } from "../../components/Kpi";
import { apiFetch, errorMessage } from "../../lib/api";
import { t, tn } from "../../i18n";
import { formatNumber, formatPercent } from "../../lib/format";
import { decisionLabel, type NowPlaying as Stream } from "../../lib/health";
import type { AsyncState } from "../../lib/hooks";
import { qualityFallback } from "../../lib/quality-fallback";
import { useTitle } from "../../lib/titles";

const clockOf = (ms: number) => {
  const total = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const sec = String(total % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${sec}` : `${m}:${sec}`;
};

/** Mbps, the unit Activity uses everywhere. */
export const mbpsOf = formatBitrate;

const isTranscoding = (s: Stream) =>
  s.decision === "transcode" || (s.decision === "copy" && !s.audioCopy);

/** One labelled fact on a stream card; hidden when there's nothing to say. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  if (children === null || children === undefined || children === "") return null;
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">{label}</dt>
      <dd className="mt-0.5 line-clamp-2 break-words text-sm text-fg">{children}</dd>
    </div>
  );
}

/** Source → output, or just the source when it's passed through unchanged. */
function Flow({ from, to }: { from: string; to: string }) {
  if (!from && !to) return null;
  if (!to || to === from) return <>{from}</>;
  return (
    <span>
      <span className="text-fg-muted">{from || "—"}</span>
      <ArrowRight size={12} className="mx-1 inline text-fg-subtle" aria-label={t("becomes")} />
      <span>{to}</span>
    </span>
  );
}

/** Cluster details share the existing card and inline list layouts. */
function ClusterDetails({ s }: { s: Stream }) {
  if (!s.cluster) return null;
  return (
    <div className="space-y-3 border-t border-border p-4">
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <Fact label={t("Source node")}>{s.cluster.source}</Fact>
        <Fact label={t("Transcoding node")}>{s.cluster.encoder}</Fact>
        <Fact label={t("Delivery")}>{s.cluster.delivery}</Fact>
      </dl>
      {s.cluster.recovery && (
        <Link
          className="inline-flex items-center gap-2 text-sm text-primary underline"
          to={s.cluster.recovery}
        >
          {s.cluster.recovering && <Badge variant="warning">{t("Recovering")}</Badge>}
          {t("Recovery history")}
        </Link>
      )}
    </div>
  );
}

function StreamCard({ s, stopping, onStop }: { s: Stream; stopping: boolean; onStop: () => void }) {
  const title = useTitle(s.ratingKey ? String(s.ratingKey) : s.key || undefined);
  const converting = isTranscoding(s);
  const elapsed = s.progress !== null && s.durationMs ? s.progress * s.durationMs : null;
  const subtitle =
    title?.type === "episode" && title.showTitle
      ? `${title.showTitle} · ${t("S{season} E{episode}", { season: title.season ?? 0, episode: title.episode ?? 0 })}`
      : title?.year
        ? String(title.year)
        : "";
  const rate = mbpsOf(s.bitrateKbps ? s.bitrateKbps * 1000 : s.speedBps * 8);
  return (
    <article className="overflow-hidden rounded-2xl border border-border bg-surface shadow-xs">
      <div className="relative isolate h-44 overflow-hidden">
        <Artwork
          src={title?.backdrop || title?.poster}
          alt=""
          title=""
          className="absolute inset-0 -z-10 size-full scale-105"
          imgClassName="size-full object-cover"
        />
        <div className="absolute inset-0 -z-10 bg-gradient-to-t from-black/90 via-black/55 to-black/20" />
        <div className="flex h-full items-end gap-4 p-4">
          <Artwork
            src={title?.poster}
            alt={s.title}
            title=""
            className="aspect-[2/3] w-20 shrink-0 rounded-lg shadow-lg ring-1 ring-white/15"
          />
          <div className="min-w-0 flex-1 space-y-2 pb-0.5">
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant={converting ? "warning" : "success"}>{decisionLabel(s)}</Badge>
              {s.failovers ? (
                <Badge variant="warning">
                  {tn(s.failovers, "{count} failover", "{count} failovers")}
                </Badge>
              ) : null}
            </div>
            <div className="min-w-0">
              <h3 className="truncate text-lg font-semibold text-on-scrim">
                {title?.title || s.title}
              </h3>
              {subtitle ? <p className="truncate text-sm text-on-scrim/75">{subtitle}</p> : null}
            </div>
            {s.progress !== null ? (
              <Meter
                onScrim
                value={s.progress}
                buffered={s.buffered}
                label={
                  elapsed !== null
                    ? t("{elapsed} of {total}", {
                        elapsed: clockOf(elapsed),
                        total: clockOf(s.durationMs),
                      })
                    : undefined
                }
                valueLabel={progressLabel(s.progress, s.buffered)}
              />
            ) : (
              <p className="text-xs text-on-scrim/75">{t("Waiting for playback position")}</p>
            )}
          </div>
          <Button
            variant="outline"
            size="sm"
            className="shrink-0 self-start border-white/25 bg-black/30 text-on-scrim hover:bg-black/50"
            disabled={stopping}
            onClick={onStop}
            aria-label={t("Stop stream for {viewer}", { viewer: s.user })}
          >
            <Square size={13} /> {t("Stop")}
          </Button>
        </div>
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 p-4 sm:grid-cols-3">
        <Fact label={t("Viewer")}>
          <span className="inline-flex min-w-0 items-center gap-2">
            <span className="grid size-5 shrink-0 place-items-center rounded-full bg-primary-subtle text-[10px] font-semibold uppercase text-primary">
              {s.user.slice(0, 1)}
            </span>
            <span className="truncate">{s.user}</span>
          </span>
        </Fact>
        <Fact label={t("Device")}>
          {s.device}
          {s.product && s.product !== s.device ? (
            <span className="text-fg-muted"> · {s.product}</span>
          ) : null}
        </Fact>
        <Fact label={t("Network")}>
          <span className="inline-flex min-w-0 items-center gap-1.5">
            {s.remote ? (
              <Globe size={13} className="shrink-0 text-info" />
            ) : (
              <Home size={13} className="shrink-0 text-success" />
            )}
            <span className="truncate">
              {s.remote ? t("Away") : t("Home")}
              {s.ip ? <span className="font-mono text-xs text-fg-muted"> · {s.ip}</span> : null}
            </span>
          </span>
        </Fact>
        <Fact label={t("Video")}>
          <Flow from={s.sourceVideo} to={s.outputVideo} />
        </Fact>
        <Fact label={t("Audio")}>
          <Flow from={s.sourceAudio} to={s.outputAudio} />
        </Fact>
        <Fact label={t("Bitrate")}>
          <span className="tabular-nums">{rate}</span>
        </Fact>
        <Fact label={t("Subtitles")}>{s.subtitle || null}</Fact>
        <Fact label={t("Source")}>{s.origin || null}</Fact>
        <Fact label={t("App")}>{s.protocol}</Fact>
      </dl>
      <ClusterDetails s={s} />
      {converting && s.decisionReason ? (
        <p className="border-t border-border px-4 py-2.5 text-xs text-fg-muted">
          <Cpu size={12} className="mr-1.5 inline text-warning" aria-hidden />
          {s.decisionReason}
        </p>
      ) : null}
      {s.release ? (
        <p
          className="truncate border-t border-border px-4 py-2 font-mono text-[11px] text-fg-subtle"
          title={s.release}
        >
          {s.release}
        </p>
      ) : null}
    </article>
  );
}

/** A rich single-row view of one stream: artwork wash, who and where, the
 * conversion, the network and the position. */
function StreamRow({ s, stopping, onStop }: { s: Stream; stopping: boolean; onStop: () => void }) {
  const title = useTitle(s.ratingKey ? String(s.ratingKey) : s.key || undefined);
  const converting = isTranscoding(s);
  const elapsed = s.progress !== null && s.durationMs ? s.progress * s.durationMs : null;
  const subtitle =
    title?.type === "episode" && title.showTitle
      ? `${title.showTitle} · ${t("S{season} E{episode}", { season: title.season ?? 0, episode: title.episode ?? 0 })}`
      : title?.year
        ? String(title.year)
        : "";
  return (
    <li className="relative isolate overflow-hidden">
      <Artwork
        src={title?.backdrop || title?.poster}
        alt=""
        title=""
        className="absolute inset-y-0 left-0 -z-10 w-2/3 opacity-30 [mask-image:linear-gradient(to_right,black,transparent)]"
        imgClassName="size-full object-cover"
      />
      <div className="grid grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-x-5 gap-y-3 p-4 lg:grid-cols-[3.5rem_minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,0.9fr)_11rem_auto]">
        <Artwork
          src={title?.poster}
          alt={s.title}
          title=""
          className="aspect-[2/3] w-14 rounded-md shadow-md ring-1 ring-white/10"
        />
        <div className="min-w-0 space-y-1">
          <div className="flex min-w-0 items-center gap-2">
            <p className="truncate text-base font-semibold text-fg">{title?.title || s.title}</p>
            <Badge variant={converting ? "warning" : "success"} className="shrink-0">
              {decisionLabel(s)}
            </Badge>
          </div>
          {subtitle ? <p className="truncate text-sm text-fg-muted">{subtitle}</p> : null}
          <p className="flex min-w-0 items-center gap-1.5 text-xs text-fg-muted">
            <span className="grid size-4 shrink-0 place-items-center rounded-full bg-primary-subtle text-[9px] font-semibold uppercase text-primary">
              {(s.user || "?").slice(0, 1)}
            </span>
            <span className="truncate">
              <span className="text-fg">{s.user}</span>
              {[s.device, s.product !== s.device ? s.product : ""]
                .filter(Boolean)
                .map((v) => ` · ${v}`)}
            </span>
          </p>
        </div>
        <dl className="hidden min-w-0 space-y-1 text-xs lg:block">
          <div className="flex min-w-0 gap-2">
            <dt className="w-14 shrink-0 text-fg-subtle">{t("Video")}</dt>
            <dd className="truncate text-fg">
              <Flow from={s.sourceVideo} to={s.outputVideo} />
            </dd>
          </div>
          <div className="flex min-w-0 gap-2">
            <dt className="w-14 shrink-0 text-fg-subtle">{t("Audio")}</dt>
            <dd className="truncate text-fg">
              <Flow from={s.sourceAudio} to={s.outputAudio} />
            </dd>
          </div>
          {s.subtitle ? (
            <div className="flex min-w-0 gap-2">
              <dt className="w-14 shrink-0 text-fg-subtle">{t("Subtitles")}</dt>
              <dd className="truncate text-fg">{s.subtitle}</dd>
            </div>
          ) : null}
        </dl>
        <div className="hidden min-w-0 space-y-1 text-xs lg:block">
          <p className="flex min-w-0 items-center gap-1.5 text-fg">
            {s.remote ? (
              <Globe size={12} className="shrink-0 text-info" />
            ) : (
              <Home size={12} className="shrink-0 text-success" />
            )}
            {s.remote ? t("Away") : t("Home")}
            {s.ip ? <span className="truncate font-mono text-fg-muted">{s.ip}</span> : null}
          </p>
          <p className="truncate text-fg-muted">
            <span className="font-medium tabular-nums text-fg">
              {mbpsOf(s.bitrateKbps ? s.bitrateKbps * 1000 : s.speedBps * 8)}
            </span>
            {s.origin ? ` · ${s.origin}` : ""}
          </p>
        </div>
        <div className="col-span-2 min-w-0 lg:col-span-1">
          {s.progress !== null ? (
            <Meter
              value={s.progress}
              buffered={s.buffered}
              label={
                elapsed !== null
                  ? t("{elapsed} of {total}", {
                      elapsed: clockOf(elapsed),
                      total: clockOf(s.durationMs),
                    })
                  : undefined
              }
              valueLabel={progressLabel(s.progress, s.buffered)}
            />
          ) : (
            <p className="text-xs text-fg-muted">{t("Waiting for playback position")}</p>
          )}
        </div>
        {s.qualityFallback ? (
          <p className="text-xs text-fg-muted">{qualityFallback(s.qualityFallback)}</p>
        ) : null}
        {s.release ? (
          <p className="truncate font-mono text-[11px] text-fg-subtle" title={s.release}>
            {s.release}
          </p>
        ) : null}
        <Button
          variant="outline"
          size="sm"
          disabled={stopping}
          onClick={onStop}
          aria-label={t("Stop stream for {viewer}", { viewer: s.user })}
          className="col-start-3 row-start-1 lg:col-auto lg:row-auto"
        >
          <Square size={13} /> {t("Stop")}
        </Button>
      </div>
      <ClusterDetails s={s} />
    </li>
  );
}

/** Card or list layout for Now Playing, remembered on this browser. */
type NowView = "cards" | "list";
const VIEW_KEY = "ether.activity.nowView";
/** Watched share, plus the read-ahead buffer when it runs past the playhead. */
function progressLabel(progress: number, buffered: number) {
  return buffered > progress
    ? t("{watched} watched · {buffered} buffered", {
        watched: formatPercent(progress),
        buffered: formatPercent(buffered),
      })
    : t("{percent}% watched", { percent: Math.round(progress * 100) });
}

export function useNowView(): [NowView, (v: NowView) => void] {
  const [view, setView] = useState<NowView>(() =>
    typeof localStorage !== "undefined" && localStorage.getItem(VIEW_KEY) === "list"
      ? "list"
      : "cards",
  );
  return [
    view,
    (v) => {
      setView(v);
      try {
        localStorage.setItem(VIEW_KEY, v);
      } catch {
        // Private mode: keep the choice for this visit only.
      }
    },
  ];
}

/** The Cards / List switch on Now Playing's tab row. */
export function ViewSwitch({
  value,
  onChange,
}: {
  value: NowView;
  onChange: (v: NowView) => void;
}) {
  return (
    <Segmented
      ariaLabel={t("Layout")}
      size="sm"
      value={value}
      onChange={onChange}
      options={[
        { value: "cards", label: t("Cards"), icon: <LayoutGrid size={13} /> },
        { value: "list", label: t("List"), icon: <Rows3 size={13} /> },
      ]}
    />
  );
}

/** The protocols of the live streams, for the tab row's filter. */
const streamProtocols = (streams: Stream[]) =>
  [...new Set(streams.map((stream) => stream.protocol))].sort();

/** A protocol filter for Now Playing's tab row, shown once streams differ. */
export function ProtocolFilter({
  streams,
  value,
  onChange,
}: {
  streams: Stream[];
  value: string;
  onChange: (v: string) => void;
}) {
  const protocols = streamProtocols(streams);
  if (protocols.length < 2 && !value) return null;
  return (
    <Select
      aria-label={t("Protocol")}
      className="h-8 w-40 text-sm"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    >
      <option value="">{t("All protocols")}</option>
      {protocols.map((p) => (
        <option key={p} value={p}>
          {p}
        </option>
      ))}
    </Select>
  );
}

export function NowPlaying({
  state,
  protocol,
  view = "cards",
}: {
  state: AsyncState<Stream[]>;
  protocol: string;
  view?: NowView;
}) {
  const [stopping, setStopping] = useState<string | null>(null);
  const streams = state.data ?? [];
  const filtered = protocol ? streams.filter((stream) => stream.protocol === protocol) : streams;
  const converting = filtered.filter(isTranscoding).length;
  const throughput = filtered.reduce((sum, s) => sum + (s.speedBps || 0), 0);

  const stop = async (id: string) => {
    setStopping(id);
    try {
      await apiFetch(`/stats/sessions/${encodeURIComponent(id)}/stop`, {
        method: "POST",
        body: "{}",
      });
      toast.success(t("Stream stopped"));
      await state.reload();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setStopping(null);
    }
  };

  if (state.error && !state.data)
    return <ErrorPanel error={state.error} onRetry={() => void state.reload()} />;
  const ready = Boolean(state.data);
  return (
    <>
      <KpiRow>
        <Kpi
          label={t("Streaming now")}
          value={ready ? formatNumber(filtered.length, 0) : "—"}
          icon={<Radio size={18} />}
        />
        <Kpi
          label={t("Direct")}
          value={ready ? formatNumber(filtered.length - converting, 0) : "—"}
          detail={t("Played or streamed as-is")}
          icon={<PlayCircle size={18} />}
          tone="success"
        />
        <Kpi
          label={t("Transcoding")}
          value={ready ? formatNumber(converting, 0) : "—"}
          detail={t("Video or audio converted")}
          icon={<Cpu size={18} />}
          tone="warning"
        />
        <Kpi
          label={t("Throughput")}
          value={ready ? mbpsOf(throughput * 8) : "—"}
          icon={<Gauge size={18} />}
          tone="info"
        />
      </KpiRow>
      {!state.data ? (
        <LoadingRows rows={2} />
      ) : filtered.length ? (
        view === "list" ? (
          <ul className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-surface">
            {filtered.map((s) => (
              <StreamRow
                key={s.id || s.key}
                s={s}
                stopping={stopping === s.id}
                onStop={() => void stop(s.id)}
              />
            ))}
          </ul>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            {filtered.map((s) => (
              <StreamCard
                key={s.id || s.key}
                s={s}
                stopping={stopping === s.id}
                onStop={() => void stop(s.id)}
              />
            ))}
          </div>
        )
      ) : streams.length ? (
        <p>{t("No streams match this protocol.")}</p>
      ) : (
        <EmptyState
          icon={<Radio />}
          title={t("Nothing is playing")}
          description={t("Streams appear here as soon as a viewer presses play.")}
        />
      )}
    </>
  );
}
