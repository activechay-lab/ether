/**
 * The Studio stage widgets: every agent tool call and result becomes a live
 * moment, drawn from this closed set and filled with the call's real result
 * (see LiveStage).
 */
import { Badge, cn } from "@braedonsaunders/appkit-ui";
import { Check, TriangleAlert, Sparkles, Wrench } from "lucide-react";
import type { ReactNode } from "react";
import { Artwork } from "../../components/Artwork";
import { t } from "../../i18n";
import "./stage.css";

export type Moment =
  | { kind: "intent"; text: string; tool: string }
  | {
      kind: "found";
      tool: string;
      title: string;
      cards: { name: string; url: string; via: string }[];
    }
  | { kind: "repair"; text: string }
  | { kind: "guide"; tool: string; title: string; channels: GuideChannel[] }
  | {
      kind: "catalog";
      tool: string;
      title: string;
      items: { key: string; title: string; poster?: string }[];
      total: number;
    }
  | { kind: "checks"; title: string; items: string[]; failed?: boolean[]; issues?: string[] }
  | {
      kind: "library";
      tool: string;
      title: string;
      /** "collection → album → track", as the library page nests it. */
      levels: string;
      items: { key: string; title: string; level: string; depth: number; playable?: boolean }[];
      projections: { protocol: string; summary: string }[];
      liveSearch: boolean;
      dryRun?: { title: string; sources: { pattern: string; detail: string }[] };
      issues?: string[];
    }
  | { kind: "done"; text: string };

export interface StreamTile {
  name: string;
  score?: string;
}

export interface GuideChannel {
  number: number;
  name: string;
  now: string;
  next: string;
  progress: number;
}

export function MomentView({ moment }: { moment: Moment }) {
  switch (moment.kind) {
    case "intent":
      return (
        <div className="flex items-center gap-3">
          <span className="flex size-9 items-center justify-center rounded-full bg-primary text-primary-fg">
            <Sparkles size={17} aria-hidden />
          </span>
          <p className="text-lg font-semibold tracking-tight text-fg">{moment.text}</p>
        </div>
      );
    case "found":
      return (
        <Panel title={moment.title} tool={moment.tool}>
          <div className="grid gap-2.5 sm:grid-cols-2">
            {moment.cards.map((c, i) => (
              <div
                key={c.url}
                className="stage-pop flex items-center gap-3 rounded-lg border border-border bg-bg-subtle px-3 py-2.5"
                style={{ animationDelay: `${i * 140}ms` }}
              >
                <Monogram name={c.name} />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-fg">{c.name}</p>
                  <p className="truncate font-mono text-xs text-fg-muted">{c.url}</p>
                </div>
              </div>
            ))}
          </div>
        </Panel>
      );
    case "repair":
      return (
        <p className="flex items-center gap-2 pl-1 text-sm text-fg-muted">
          <Wrench size={14} className="shrink-0 text-primary" aria-hidden /> {moment.text}
        </p>
      );
    case "guide":
      return (
        <Panel title={moment.title} tool={moment.tool}>
          <GuideStrip channels={moment.channels} animate />
        </Panel>
      );
    case "catalog":
      return (
        <Panel title={moment.title} tool={moment.tool}>
          <PosterGrid items={moment.items} total={moment.total} />
        </Panel>
      );
    case "checks":
      return (
        <Panel title={moment.title}>
          <ul className="space-y-2">
            {moment.items.map((it, i) => (
              <li
                key={it}
                className="stage-snap flex items-center gap-2.5 text-sm text-fg"
                style={{ animationDelay: `${i * 260}ms` }}
              >
                <span
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center rounded-full",
                    moment.failed?.[i]
                      ? "bg-warning/15 text-warning"
                      : "bg-success/15 text-success",
                  )}
                >
                  {moment.failed?.[i] ? (
                    <TriangleAlert size={12} aria-hidden />
                  ) : (
                    <Check size={12} strokeWidth={3} aria-hidden />
                  )}
                </span>
                {it}
              </li>
            ))}
          </ul>
          {moment.issues?.length ? (
            <div
              role="status"
              className="mt-3 space-y-1 border-t border-border pt-3 text-sm text-warning"
            >
              {moment.issues.map((issue, i) => (
                <p key={i} className="whitespace-pre-wrap break-words">
                  {issue}
                </p>
              ))}
            </div>
          ) : null}
        </Panel>
      );
    case "library":
      return (
        <Panel title={moment.title} tool={moment.tool}>
          <p className="mb-3 text-xs font-medium uppercase tracking-wide text-fg-muted">
            {moment.levels}
          </p>
          <ul className="space-y-1.5">
            {moment.items.map((it, i) => (
              <li
                key={it.key}
                className="stage-pop flex items-center gap-2.5 rounded-lg border border-border bg-bg-subtle px-3 py-2"
                style={{ animationDelay: `${i * 120}ms`, marginLeft: `${it.depth * 1.25}rem` }}
              >
                <Monogram name={it.title} />
                <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">
                  {it.title}
                </span>
                <span className="text-xs text-fg-muted">{it.level}</span>
                {it.playable ? <Badge variant="secondary">{t("Plays")}</Badge> : null}
              </li>
            ))}
          </ul>
          <div className="mt-3 space-y-1 border-t border-border pt-3 text-sm text-fg-muted">
            {moment.projections.map((p) => (
              <p key={p.protocol}>
                <span className="font-medium text-fg">{p.protocol}</span> — {p.summary}
              </p>
            ))}
            <p>
              {moment.liveSearch
                ? t(
                    "Live search is on: searches in apps also ask this plugin, and Ether keeps what it finds.",
                  )
                : t("Live search is off: searches only look in what Ether has already synced.")}
            </p>
          </div>
          {moment.dryRun ? (
            <div className="mt-3 border-t border-border pt-3">
              <p className="text-sm font-medium text-fg">
                {t("Playing {title} would use", { title: moment.dryRun.title })}
              </p>
              <ul className="mt-1.5 space-y-1 text-sm text-fg-muted">
                {moment.dryRun.sources.map((s, i) => (
                  <li key={i} className="break-words">
                    <span className="font-medium text-fg">{s.pattern}</span> — {s.detail}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {moment.issues?.length ? (
            <div
              role="status"
              className="mt-3 space-y-1 border-t border-border pt-3 text-sm text-warning"
            >
              {moment.issues.map((issue, i) => (
                <p key={i} className="whitespace-pre-wrap break-words">
                  {issue}
                </p>
              ))}
            </div>
          ) : null}
        </Panel>
      );
    case "done":
      return (
        <div className="stage-pop flex items-center gap-3 rounded-xl border border-primary/30 bg-primary/5 px-4 py-3.5">
          <span className="flex size-8 items-center justify-center rounded-full bg-primary text-primary-fg">
            <Check size={16} strokeWidth={3} aria-hidden />
          </span>
          <p className="font-semibold text-fg">{moment.text}</p>
        </div>
      );
  }
}

function Panel({ title, children }: { title: string; tool?: string; children: ReactNode }) {
  return (
    <section
      aria-label={title}
      className="rounded-xl border border-border bg-surface p-4 shadow-sm"
    >
      <div className="mb-3 flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
      </div>
      {children}
    </section>
  );
}

function Monogram({ name }: { name: string }) {
  const letters = name
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0])
    .join("");
  return (
    <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-xs font-bold text-primary">
      {letters}
    </span>
  );
}

export function GuideStrip({ channels, animate }: { channels: GuideChannel[]; animate?: boolean }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border">
      <div className="grid grid-cols-[3.5rem_10rem_1fr_1fr] border-b border-border bg-bg-subtle px-3 py-1.5 text-[11px] font-medium text-fg-subtle">
        <span />
        <span />
        <span>{t("Now")}</span>
        <span>{t("Next")}</span>
      </div>
      {channels.map((c, i) => (
        <div
          key={c.number}
          className={cn(
            "grid grid-cols-[3.5rem_10rem_1fr_1fr] items-center gap-2 border-b border-border-subtle px-3 py-2 last:border-b-0",
            animate && "stage-snap",
          )}
          style={animate ? { animationDelay: `${i * 320}ms` } : undefined}
        >
          <span className="font-semibold tabular-nums text-fg">{c.number}</span>
          <span className="flex min-w-0 items-center gap-2">
            <Monogram name={c.name} />
            <span className="truncate text-sm text-fg">{c.name}</span>
          </span>
          <span className="min-w-0 rounded-md bg-primary/10 px-2 py-1.5">
            <span className="block truncate text-xs font-medium text-fg">{c.now}</span>
            <span className="mt-1 block h-1 overflow-hidden rounded-full bg-primary/15">
              <span
                className="block h-full rounded-full bg-primary"
                style={{ width: `${c.progress * 100}%` }}
              />
            </span>
          </span>
          <span className="min-w-0 truncate rounded-md bg-bg-subtle px-2 py-1.5 text-xs text-fg-muted">
            {c.next}
          </span>
        </div>
      ))}
    </div>
  );
}

function PosterGrid({
  items,
  total,
}: {
  items: { key: string; title: string; poster?: string }[];
  total: number;
}) {
  return (
    <>
      <div className="grid grid-cols-6 gap-2.5 sm:grid-cols-9">
        {items.slice(0, 18).map((it, i) => (
          <div key={it.key} className="stage-flow" style={{ animationDelay: `${i * 140}ms` }}>
            <Artwork
              src={it.poster}
              alt={it.title}
              title={it.title}
              className="aspect-[2/3] w-full overflow-hidden rounded-md shadow-sm ring-1 ring-border-subtle"
            />
          </div>
        ))}
      </div>
      <div className="mt-3 flex justify-end">
        <Badge variant="secondary">{t("{count} titles", { count: total })}</Badge>
      </div>
    </>
  );
}
