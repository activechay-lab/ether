import { Badge, Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { Puzzle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { TestButton } from "../../components/fields";
import { Section } from "../../components/layout";
import { SettingsRow } from "../../components/SettingsRow";
import { SortableList } from "../../components/Sortable";
import { t, tn } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { isPluginDescriptor, textOf, useRegistry, type Descriptor } from "../../lib/extend";
import { useAsync } from "../../lib/hooks";
import { SourceCard } from "./cards";
import { OrderButtons, swap, useTests } from "./form";

type SourceClass = "debrid" | "usenet" | "indexer" | "direct";

type SourceKindMeta = {
  class?: SourceClass | string;
  abilities?: string[];
  limits?: {
    searchesPerDay?: number;
    grabsPerDay?: number;
  };
};

type PluginRecord = {
  id: string;
  name?: string;
  enabled?: boolean;
  state?: string;
  health?: string;
  healthReason?: string;
  lastError?: string;
};

function normalizeClass(value: string | undefined): SourceClass {
  switch (value) {
    case "debrid":
    case "usenet":
    case "indexer":
    case "direct":
      return value;
    default:
      return "indexer";
  }
}

function classBadgeLabel(cls: SourceClass): string {
  switch (cls) {
    case "debrid":
      return t("Debrid plugin");
    case "usenet":
      return t("Usenet plugin");
    case "direct":
      return t("Direct source");
    case "indexer":
    default:
      return t("Stream plugin");
  }
}

function abilityLabel(ability: string): string {
  switch (ability) {
    case "cache-check":
      return t("Cache checks");
    case "cached-playback":
      return t("Cached playback");
    case "search":
      return t("Title search");
    case "movie-search":
      return t("Movies");
    case "tv-search":
      return t("TV episodes");
    case "Direct":
    case "direct":
      return t("Direct streams");
    default:
      return ability;
  }
}

function limitsSummary(limits: SourceKindMeta["limits"] | undefined): string[] {
  if (!limits) return [];
  const out: string[] = [];
  const searches = Number(limits.searchesPerDay ?? 0);
  const grabs = Number(limits.grabsPerDay ?? 0);
  if (searches > 0) {
    out.push(tn(searches, "{count} search a day", "{count} searches a day"));
  }
  if (grabs > 0) {
    out.push(tn(grabs, "{count} grab a day", "{count} grabs a day"));
  }
  return out;
}

function PluginStateBadge({ plugin }: { plugin?: PluginRecord }) {
  const state = plugin?.health ?? plugin?.state;
  if (plugin && plugin.enabled === false) {
    return <Badge variant="secondary">{t("Off")}</Badge>;
  }
  switch (state) {
    case "attention":
    case "faulted":
    case "error":
    case "Quarantined":
    case "quarantined":
      return <Badge variant="destructive">{t("Needs attention")}</Badge>;
    case "degraded":
    case "Degraded":
    case "backoff":
      return <Badge variant="warning">{t("Degraded")}</Badge>;
    case "disabled":
    case "Disabled":
      return <Badge variant="secondary">{t("Off")}</Badge>;
    default:
      return <Badge variant="success">{t("Ready")}</Badge>;
  }
}

async function testPluginSource(
  pluginId: string,
): Promise<{ ok: boolean; error?: string; durationMs: number }> {
  const started = Date.now();
  const res = await apiFetch<{
    ok?: boolean;
    message?: string;
    error?: string;
    durationMs?: number;
    latencyMs?: number;
  }>(`/plugins/${encodeURIComponent(pluginId)}/test`, {
    method: "POST",
  });
  return {
    ok: res?.ok ?? true,
    error: res?.error ?? res?.message,
    durationMs: res?.durationMs ?? res?.latencyMs ?? Date.now() - started,
  };
}

/**
 * Renders plugin-contributed `sourceKind` descriptors matching `classes`
 * using the standard `SourceCard`, `OrderButtons` and `TestButton` controls.
 */
export function PluginSourceCards({
  classes,
  sectionTitle,
  sectionDescription,
}: {
  classes: SourceClass[];
  sectionTitle?: string;
  sectionDescription?: string;
}) {
  const { descriptors } = useRegistry<SourceKindMeta>("sourceKind");
  const pluginsQuery = useAsync(
    () => apiFetch<{ plugins?: PluginRecord[] } | PluginRecord[]>("/plugins").catch(() => null),
    [],
  );
  const { tests, runTest } = useTests();
  const [orderIds, setOrderIds] = useState<string[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);

  const matching = useMemo(() => {
    const allowed = new Set<SourceClass>(classes);
    return descriptors.filter(
      (d) => isPluginDescriptor(d) && allowed.has(normalizeClass(d.meta?.class)),
    );
  }, [descriptors, classes]);

  useEffect(() => {
    setOrderIds((prev) => {
      const nextIds = matching.map((d) => d.id);
      const kept = prev.filter((id) => nextIds.includes(id));
      for (const id of nextIds) {
        if (!kept.includes(id)) kept.push(id);
      }
      return kept;
    });
  }, [matching]);

  const ordered = useMemo(() => {
    if (!orderIds.length) return matching;
    const byId = new Map<string, Descriptor<SourceKindMeta>>(matching.map((d) => [d.id, d]));
    const list: Descriptor<SourceKindMeta>[] = [];
    for (const id of orderIds) {
      const item = byId.get(id);
      if (item) list.push(item);
    }
    for (const item of matching) {
      if (!list.includes(item)) list.push(item);
    }
    return list;
  }, [matching, orderIds]);

  const pluginsById = useMemo(() => {
    const map = new Map<string, PluginRecord>();
    const raw = pluginsQuery.data;
    const list = Array.isArray(raw) ? raw : (raw?.plugins ?? []);
    for (const p of list) {
      if (p && typeof p.id === "string") {
        map.set(p.id, p);
      }
    }
    return map;
  }, [pluginsQuery.data]);

  if (ordered.length === 0) return null;

  const active = ordered.find((d) => d.id === activeId) ?? null;
  const activePlugin = active ? pluginsById.get(active.owner) : undefined;
  const activeLabel = active ? textOf(active.label) || active.owner : "";
  const activeClass = active ? normalizeClass(active.meta?.class) : "indexer";
  const activeAbilities = active?.meta?.abilities ?? [];
  const activeLimits = limitsSummary(active?.meta?.limits);

  function move(index: number, delta: -1 | 1) {
    const next = swap(ordered, index, index + delta);
    setOrderIds(next.map((d) => d.id));
    toast.success(t("Source order updated"));
  }

  return (
    <>
      <Section
        title={sectionTitle ?? t("Plugin sources")}
        description={
          sectionDescription ??
          t(
            "Stream sources contributed by installed plugins. Results rank by your quality preferences alongside built-in sources.",
          )
        }
        bodyClassName="space-y-3 p-4"
      >
        <SortableList
          items={ordered}
          getId={(d) => d.id}
          className="space-y-3"
          onReorder={(next) => {
            setOrderIds(next.map((d) => d.id));
            toast.success(t("Source order updated"));
          }}
          renderItem={(d, handle, i) => {
            const label = textOf(d.label) || d.owner;
            const hint = textOf(d.hint);
            const cls = normalizeClass(d.meta?.class);
            const abilities = (d.meta?.abilities ?? []).map(abilityLabel);
            const limits = limitsSummary(d.meta?.limits);
            const nativeDebrid = cls === "debrid" && d.id.endsWith("/debrid");
            const plugin = pluginsById.get(d.owner);
            const lastError = plugin?.healthReason || plugin?.lastError;
            const testState = tests[d.owner];
            const tested =
              testState?.status === "ok"
                ? { ok: true, message: undefined }
                : testState?.status === "fail"
                  ? { ok: false, message: testState.message }
                  : undefined;

            const details = [
              nativeDebrid
                ? t("Uses native cache checks and playback")
                : i === 0
                  ? t("Tried first")
                  : t("Priority {number}", { number: i + 1 }),
              hint,
              ...abilities,
              ...limits,
            ]
              .filter(Boolean)
              .join(" · ");

            return (
              <SourceCard
                name={label}
                position={i + 1}
                description={details}
                problem={lastError ?? null}
                logo={<Puzzle size={18} />}
                meta={
                  <>
                    <Badge variant="outline">{classBadgeLabel(cls)}</Badge>
                    <PluginStateBadge plugin={plugin} />
                    {tested ? (
                      <Badge variant={tested.ok ? "success" : "destructive"} title={tested.message}>
                        {tested.ok ? t("Test passed") : t("Test failed")}
                      </Badge>
                    ) : null}
                  </>
                }
                order={
                  nativeDebrid ? undefined : (
                    <OrderButtons
                      label={label}
                      i={i}
                      n={ordered.length}
                      onMove={(dir) => move(i, dir)}
                    />
                  )
                }
                handle={nativeDebrid ? undefined : handle}
                editLabel={t("Edit {name}", { name: label })}
                onEdit={() => setActiveId(d.id)}
              />
            );
          }}
        />
      </Section>

      <Drawer
        open={active !== null}
        onClose={() => setActiveId(null)}
        title={activeLabel}
        description={classBadgeLabel(activeClass)}
        footer={
          active ? (
            <div className="flex w-full items-center justify-between gap-2">
              <TestButton
                state={tests[active.owner] ?? { status: "idle" }}
                onTest={() => runTest(active.owner, () => testPluginSource(active.owner))}
              />
              <div className="flex items-center gap-2">
                <Button variant="outline" size="sm" onClick={() => setActiveId(null)}>
                  {t("Close")}
                </Button>
                <Button asChild size="sm">
                  <Link to={`/plugins/installed?plugin=${encodeURIComponent(active.owner)}`}>
                    {t("Manage in Plugins")}
                  </Link>
                </Button>
              </div>
            </div>
          ) : null
        }
      >
        {active ? (
          <SettingsSection title={t("Plugin source")}>
            <SettingsRow
              title={t("Plugin ID")}
              control={<span className="font-mono text-xs text-fg-muted">{active.owner}</span>}
            />
            <SettingsRow title={t("Status")} control={<PluginStateBadge plugin={activePlugin} />} />
            {activeAbilities.length > 0 ? (
              <SettingsRow
                title={t("Abilities")}
                control={
                  <div className="flex flex-wrap justify-end gap-1.5">
                    {activeAbilities.map((ab) => (
                      <Badge key={ab} variant="outline">
                        {abilityLabel(ab)}
                      </Badge>
                    ))}
                  </div>
                }
              />
            ) : null}
            {activeLimits.length > 0 ? (
              <SettingsRow
                title={t("Declared limits")}
                control={<span className="text-xs text-fg-muted">{activeLimits.join(" · ")}</span>}
              />
            ) : null}
          </SettingsSection>
        ) : null}
      </Drawer>
    </>
  );
}

/**
 * Displays plugin-contributed stream sources and their declared daily budgets
 * on the Sources -> Usage tab.
 */
export function PluginSourceUsage() {
  const { descriptors } = useRegistry<SourceKindMeta>("sourceKind");
  const plugins = useMemo(() => descriptors.filter((d) => isPluginDescriptor(d)), [descriptors]);

  if (plugins.length === 0) return null;

  return (
    <Section
      title={t("Plugin sources")}
      description={t(
        "Stream sources contributed by enabled plugins and their declared daily caps.",
      )}
      bodyClassName="divide-y divide-border"
    >
      {plugins.map((d) => {
        const label = textOf(d.label) || d.owner;
        const cls = normalizeClass(d.meta?.class);
        const abilities = (d.meta?.abilities ?? []).map(abilityLabel);
        const limits = limitsSummary(d.meta?.limits);
        return (
          <div key={d.id} className="flex flex-wrap items-start justify-between gap-4 px-5 py-4">
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="font-medium text-fg">{label}</p>
                <Badge variant="outline">{classBadgeLabel(cls)}</Badge>
              </div>
              <p className="text-xs text-fg-muted">
                {[...abilities, ...(limits.length ? limits : [t("Automatic rate limits")])]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            </div>
          </div>
        );
      })}
    </Section>
  );
}
