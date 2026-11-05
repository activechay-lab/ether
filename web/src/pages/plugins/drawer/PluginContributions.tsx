import { pluginCollectionHref } from "../../collections/links";
import { Badge, Switch, cn, toast } from "@braedonsaunders/appkit-ui";
import {
  ArrowUpRight,
  BookOpen,
  Film,
  FolderSearch,
  Library,
  Subtitles,
  Tv,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { addonsApi } from "../../../components/addons/addons";
import { t, tn } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { invalidateRegistry, textOf, useRegistry } from "../../../lib/extend";
import { formatNumber } from "../../../lib/format";
import type { PluginDetail } from "../types";
import { callImdb, resultCount, type PluginCall } from "./calls";
import { contributionsOf, liveTVKind, type Contribution } from "./kinds";

const META: Record<
  Contribution,
  { icon: LucideIcon; title: () => string; about: () => string; href?: string; link?: () => string }
> = {
  livetv_channels: {
    icon: Tv,
    title: () => t("Live TV channels"),
    about: () => t("Its channels and programme guide, in Live TV."),
    href: "/livetv",
    link: () => t("Open Live TV"),
  },
  list_source: {
    icon: Library,
    title: () => t("Catalogues"),
    about: () => t("Lists that fill collections and Home rows, refreshed in the background."),
    href: "/collections/new",
    link: () => t("Make a collection"),
  },
  debrid_provider: {
    icon: Film,
    title: () => t("Debrid provider"),
    about: () =>
      t("Checks cached torrents and supplies playable files through the native debrid pipeline."),
    href: "/sources",
    link: () => t("Open Sources"),
  },
  stream_source: {
    icon: Film,
    title: () => t("Sources"),
    about: () => t("Searched for a playable copy when someone presses play."),
    href: "/sources",
    link: () => t("Open Sources"),
  },
  subtitles: {
    icon: Subtitles,
    title: () => t("Subtitles"),
    about: () =>
      t("Offered beside your other subtitle providers; a file downloads only when picked."),
    href: "/playback/subtitles",
    link: () => t("Open subtitle providers"),
  },
  metadata: {
    icon: BookOpen,
    title: () => t("Title details"),
    about: () => t("Fills in details for titles its catalogues add that Ether doesn't know yet."),
    href: "/metadata",
    link: () => t("Open Metadata"),
  },
  plugin_directory: {
    icon: FolderSearch,
    title: () => t("Plugin directories"),
    about: () => t("Lists of more plugins you can browse from Plugins → Add."),
  },
};

/**
 * Everything the plugin adds to Ether, grouped by surface. Add-ons switch
 * each part on or off here; every row says where it is used.
 */
export function PluginContributions({
  detail,
  calls,
  onChanged,
  onClose,
}: {
  detail: PluginDetail;
  calls: PluginCall[] | null;
  onChanged: () => void;
  onClose: () => void;
}) {
  const parts = contributionsOf(detail);
  const [roles, setRoles] = useState(detail.roles ?? []);
  const [busy, setBusy] = useState<string | null>(null);
  useEffect(() => setRoles(detail.roles ?? []), [detail.roles]);

  const usage = useCollectionUsage(detail.id);

  const toggle = async (role: string, on: boolean) => {
    const off = roles.filter((r) => (r.role === role ? !on : !r.on)).map((r) => r.role);
    setBusy(role);
    try {
      const res = await addonsApi.setRoles(detail.id, off);
      setRoles(res.roles);
      invalidateRegistry();
      onChanged();
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-4">
      {parts.map((part) => {
        const meta = META[part];
        const role = roles.find((r) => r.role === part);
        const on = role ? role.on : true;
        return (
          <section
            key={part}
            aria-label={meta.title()}
            className={cn(
              "overflow-hidden rounded-xl border border-border bg-surface transition-opacity",
              (!on || !detail.enabled) && "opacity-70",
            )}
          >
            <header className="flex items-start gap-3 px-5 py-4">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                <meta.icon size={18} aria-hidden />
              </span>
              <div className="min-w-0 flex-1">
                <h3 className="text-sm font-semibold text-fg">{meta.title()}</h3>
                <p className="mt-0.5 text-sm text-fg-muted">{meta.about()}</p>
              </div>
              {role ? (
                <Switch
                  aria-label={t("Use {part}", { part: meta.title() })}
                  checked={role.on}
                  disabled={busy !== null || !detail.enabled}
                  onChange={(e) => void toggle(part, e.currentTarget.checked)}
                />
              ) : null}
            </header>
            {on && detail.enabled ? (
              <div className="border-t border-border">
                <PartRows
                  part={part}
                  detail={detail}
                  calls={calls}
                  usage={usage}
                  onClose={onClose}
                />
              </div>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}

function useCollectionUsage(pluginId: string) {
  const [usage, setUsage] = useState<Record<string, number>>({});
  useEffect(() => {
    let live = true;
    apiFetch<{ collections: { sources?: { kind?: string }[] }[] }>("/collections")
      .then((r) => {
        const out: Record<string, number> = {};
        for (const c of r.collections ?? []) {
          const kinds = new Set((c.sources ?? []).map((s) => s.kind ?? ""));
          for (const k of kinds)
            if (k.startsWith(`plugin:${pluginId}/`)) out[k] = (out[k] ?? 0) + 1;
        }
        if (live) setUsage(out);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [pluginId]);
  return usage;
}

function PartRows({
  part,
  detail,
  calls,
  usage,
  onClose,
}: {
  part: Contribution;
  detail: PluginDetail;
  calls: PluginCall[] | null;
  usage: Record<string, number>;
  onClose: () => void;
}) {
  const meta = META[part];
  const { descriptors } = useRegistry();
  const owned = useMemo(
    () => descriptors.filter((d) => d.owner === detail.id),
    [descriptors, detail.id],
  );
  const link = meta.href ? <RowLink to={meta.href} label={meta.link!()} onClose={onClose} /> : null;

  switch (part) {
    case "list_source": {
      const lists = owned.filter((d) => d.kind === "collectionSource");
      if (!lists.length)
        return (
          <ul>
            <Row title={t("No catalogues right now")} />
          </ul>
        );
      return (
        <ul className="divide-y divide-border">
          {lists.map((d) => {
            const [name, type] = textOf(d.label)
              .split("·")
              .map((s) => s.trim());
            const used = usage[d.id] ?? 0;
            return (
              <Row
                key={d.id}
                title={name ?? textOf(d.label)}
                badge={type ? <Badge variant="secondary">{typeLabel(type)}</Badge> : null}
                detail={
                  used
                    ? tn(used, "Used by {count} collection", "Used by {count} collections")
                    : t("Not used yet")
                }
                action={
                  <RowLink
                    to={pluginCollectionHref(d.id, textOf(d.label))}
                    label={t("Make a collection")}
                    onClose={onClose}
                  />
                }
              />
            );
          })}
        </ul>
      );
    }
    case "livetv_channels":
      return <LiveTVRow detail={detail} onClose={onClose} />;
    case "debrid_provider":
      return (
        <ul>
          <Row
            title={t("Native debrid provider")}
            detail={t("Configure its account in Setup, then test the connection.")}
            action={link}
          />
        </ul>
      );
    case "stream_source":
    case "subtitles":
    case "metadata": {
      const resource =
        part === "stream_source" ? "stream" : part === "subtitles" ? "subtitles" : "meta";
      const mine = (calls ?? []).filter(
        (c) =>
          c.route.startsWith(`/${resource}/`) &&
          !c.route.startsWith("/stream/tv/") &&
          callImdb(c.route),
      );
      const found = mine.filter((c) => (resultCount(c) ?? (c.status === 200 ? 1 : 0)) > 0).length;
      const failed = mine.filter((c) => c.status !== 200).length;
      const summary = mine.length
        ? part === "metadata"
          ? tn(mine.length, "{count} recent lookup", "{count} recent lookups")
          : t("{found} of {total} recent lookups found something", {
              found: formatNumber(found, 0),
              total: formatNumber(mine.length, 0),
            })
        : t("Not asked yet");
      return (
        <ul>
          <Row
            title={summary}
            detail={failed ? tn(failed, "{count} failed", "{count} failed") : undefined}
            action={link}
          />
        </ul>
      );
    }
    case "plugin_directory": {
      const dirs = owned.filter((d) => d.kind === "pluginDirectory");
      return (
        <ul>
          <Row
            title={tn(dirs.length, "{count} directory", "{count} directories")}
            detail={dirs
              .slice(0, 4)
              .map((d) => textOf(d.label))
              .join(", ")}
          />
        </ul>
      );
    }
  }
}

function typeLabel(type: string): string {
  switch (type) {
    case "movie":
      return t("Movies");
    case "series":
      return t("TV shows");
    default:
      return type;
  }
}

interface LiveSource {
  id: string;
  kind: string;
  channelCount?: number;
  guideChannelCount?: number;
  updatedAt?: string;
}

function LiveTVRow({ detail, onClose }: { detail: PluginDetail; onClose: () => void }) {
  const [source, setSource] = useState<LiveSource | null | undefined>(undefined);
  const [adding, setAdding] = useState(false);
  const kind = liveTVKind(detail.id);
  const load = () =>
    apiFetch<LiveSource[]>("/livetv/sources")
      .then((s) => setSource(s.find((x) => x.kind === kind) ?? null))
      .catch(() => setSource(null));
  useEffect(() => {
    void load();
  }, [kind]);
  if (source === undefined)
    return (
      <ul>
        <Row title={t("Loading…")} />
      </ul>
    );
  if (source === null) {
    return (
      <ul>
        <Row
          title={t("Not in Live TV yet")}
          action={
            <button
              type="button"
              disabled={adding}
              className="text-xs font-medium text-primary hover:underline"
              onClick={() => {
                setAdding(true);
                void apiFetch("/livetv/sources", {
                  method: "POST",
                  body: JSON.stringify({ name: detail.name, kind, playlistUrl: kind }),
                })
                  .then(load)
                  .catch((e) => toast.error(errorMessage(e)))
                  .finally(() => setAdding(false));
              }}
            >
              {adding ? t("Adding…") : t("Add to Live TV")}
            </button>
          }
        />
      </ul>
    );
  }
  return (
    <ul>
      <Row
        title={tn(source.channelCount ?? 0, "{count} channel", "{count} channels")}
        detail={tn(
          source.guideChannelCount ?? 0,
          "Guide for {count} channel",
          "Guide for {count} channels",
        )}
        action={<RowLink to="/livetv" label={t("Open Live TV")} onClose={onClose} />}
      />
    </ul>
  );
}

function Row({
  title,
  badge,
  detail,
  action,
}: {
  title: ReactNode;
  badge?: ReactNode;
  detail?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <li className="flex items-center justify-between gap-3 px-5 py-3">
      <div className="min-w-0">
        <p className="flex items-center gap-2 text-sm font-medium text-fg">
          <span className="truncate">{title}</span>
          {badge}
        </p>
        {detail ? <p className="mt-0.5 truncate text-xs text-fg-muted">{detail}</p> : null}
      </div>
      {action ? <div className="shrink-0">{action}</div> : null}
    </li>
  );
}

function RowLink({ to, label, onClose }: { to: string; label: string; onClose: () => void }) {
  return (
    <Link
      to={to}
      onClick={onClose}
      className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
    >
      {label} <ArrowUpRight size={12} aria-hidden />
    </Link>
  );
}
