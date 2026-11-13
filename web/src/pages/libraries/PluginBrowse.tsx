import { Button, EmptyState } from "@braedonsaunders/appkit-ui";
import { ChevronRight, Library as LibraryIcon, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { t, tn } from "../../i18n";
import { formatNumber } from "../../lib/format";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import { artUrl, librariesApi, type KindItem, type Library } from "./api";
import { PluginItemDrawer } from "./PluginItemDrawer";

const SORT_LABELS: Record<string, () => string> = {
  title: () => t("Title A–Z"),
  added: () => t("Recently added"),
  released: () => t("Recently released"),
  year: () => t("Year"),
  rating: () => t("Highest rated"),
  index: () => t("In order"),
};

/**
 * Browse one plugin library (spec §11): hierarchy with breadcrumbs,
 * search, declared sorts and facet filters, items opening the plugin
 * item drawer. State lives in the URL like the catalogue browser.
 */
export function PluginBrowse({
  library,
  ruleFilter,
  controlledParams,
  onParams,
}: {
  library: Library;
  ruleFilter?: Record<string, string>;
  controlledParams?: URLSearchParams;
  onParams?: (p: URLSearchParams) => void;
}) {
  const [urlParams, setUrlParams] = useSearchParams();
  const params = controlledParams ?? urlParams;
  const setParams = onParams ?? setUrlParams;
  const kind = library.kind;
  const query = params.get("q") ?? "";
  const debouncedQuery = useDebouncedValue(query, 300);
  const parent = params.get("parent") ?? "";
  const parentTitle = params.get("parentTitle") ?? "";
  const sort = params.get("sort") || kind?.browse.sorts?.[0] || "title";
  const selected = params.get("item") ?? "";
  const facetValues = useMemo(() => {
    const out: Record<string, string> = {};
    for (const f of kind?.browse.facets ?? []) out[f] = params.get(`facet.${f}`) ?? "";
    return out;
  }, [params, kind]);
  const [extraPages, setExtraPages] = useState<{ key: string; items: KindItem[]; cursor: string }>({
    key: "",
    items: [],
    cursor: "",
  });
  const [loadingMore, setLoadingMore] = useState(false);

  const requestKey = JSON.stringify([debouncedQuery, parent, sort, facetValues, ruleFilter]);
  const page = useAsync(
    (signal) => {
      const p: Record<string, string> = { sort, limit: "48", ...ruleFilter };
      if (debouncedQuery) p.q = debouncedQuery;
      if (parent) p.parent = parent;
      for (const [k, v] of Object.entries(facetValues)) if (v) p[`facet.${k}`] = v;
      return librariesApi.items(library.id, p, signal);
    },
    [library.id, requestKey],
  );

  const update = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    setParams(next);
  };

  const shown: KindItem[] = [
    ...(page.data?.items ?? []),
    ...(extraPages.key === requestKey ? extraPages.items : []),
  ];
  const nextCursor =
    extraPages.key === requestKey && extraPages.cursor
      ? extraPages.cursor
      : (page.data?.nextCursor ?? "");

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const p: Record<string, string> = { sort, limit: "48", cursor: nextCursor, ...ruleFilter };
      if (parent) p.parent = parent;
      if (debouncedQuery) p.q = debouncedQuery;
      for (const [k, v] of Object.entries(facetValues)) if (v) p[`facet.${k}`] = v;
      const more = await librariesApi.items(library.id, p);
      setExtraPages((prev) => ({
        key: requestKey,
        items: [...(prev.key === requestKey ? prev.items : []), ...more.items],
        cursor: more.nextCursor ?? "",
      }));
    } finally {
      setLoadingMore(false);
    }
  };

  const openItem = (item: KindItem) => {
    if (!item.playable && (item.childCount ?? 0) > 0) {
      update({ parent: item.ref, parentTitle: item.title, item: "" });
    } else {
      update({ item: item.ref });
    }
  };

  const sorts = (kind?.browse.sorts?.length ? kind.browse.sorts : ["title"]).map((s) => ({
    value: s,
    label: SORT_LABELS[s]?.() ?? s,
  }));

  return (
    <div className="plugin-browse">
      <div className="plugin-browse-bar">
        <nav aria-label={t("Inside this library")} className="plugin-crumbs">
          <button type="button" onClick={() => update({ parent: "", parentTitle: "", item: "" })}>
            {library.name}
          </button>
          {parent ? (
            <>
              <ChevronRight size={13} aria-hidden />
              <span aria-current="page">{parentTitle || t("Inside")}</span>
            </>
          ) : null}
        </nav>
        <span className="flex-1" />
        {kind?.browse.search ? (
          <label className="plugin-search">
            <Search size={14} aria-hidden />
            <input
              value={query}
              onChange={(e) => update({ q: e.target.value })}
              placeholder={t("Search {name}", { name: library.name })}
              aria-label={t("Search {name}", { name: library.name })}
            />
          </label>
        ) : null}
        {(kind?.browse.facets ?? []).map((field) => (
          <FacetPicker
            key={field}
            library={library}
            field={field}
            value={facetValues[field] ?? ""}
            onChange={(v) => update({ [`facet.${field}`]: v })}
          />
        ))}
        <ToolbarPicker
          ariaLabel={t("Sort items")}
          value={sort}
          options={sorts}
          onChange={(v) => update({ sort: v })}
        />
      </div>
      <p role="status" className="text-sm text-fg-muted">
        {page.loading
          ? t("Loading…")
          : page.data
            ? tn(page.data.total, "{count} item", "{count} items", {
                count: formatNumber(page.data.total, 0),
              })
            : ""}
      </p>
      {page.error ? <ErrorPanel error={page.error} onRetry={() => void page.reload()} /> : null}
      {page.loading && !page.data ? <LoadingRows /> : null}
      {!page.loading && page.data && shown.length === 0 ? (
        <EmptyState
          icon={<LibraryIcon size={22} aria-hidden />}
          title={debouncedQuery ? t("Nothing matches") : t("Nothing here yet")}
          description={
            debouncedQuery
              ? t("Try a different search.")
              : t(
                  "Items appear after the plugin syncs. Use Sync now in Settings to fetch them right away.",
                )
          }
        />
      ) : (
        <ul className="plugin-grid">
          {shown.map((item) => (
            <li key={item.ref}>
              <button type="button" className="plugin-card" onClick={() => openItem(item)}>
                {artUrl(item) ? (
                  <img src={artUrl(item)} alt="" loading="lazy" />
                ) : (
                  <span className="plugin-card-fallback" aria-hidden>
                    {item.title.slice(0, 1)}
                  </span>
                )}
                <span className="plugin-card-title">{item.title}</span>
                <span className="plugin-card-meta">
                  {kind?.levels.find((l) => l.key === item.level)?.label ?? item.level}
                  {item.year ? ` · ${item.year}` : ""}
                  {(item.childCount ?? 0) > 0
                    ? ` · ${tn(item.childCount ?? 0, "{count} inside", "{count} inside")}`
                    : ""}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {nextCursor ? (
        <div className="mt-4">
          <Button variant="outline" disabled={loadingMore} onClick={() => void loadMore()}>
            {t("Show more")}
          </Button>
        </div>
      ) : null}
      {selected ? (
        <PluginItemDrawer
          library={library}
          itemRef={selected}
          onClose={() => update({ item: "" })}
          onOpenItem={(ref) => update({ item: ref })}
        />
      ) : null}
    </div>
  );
}

function FacetPicker({
  library,
  field,
  value,
  onChange,
}: {
  library: Library;
  field: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const facets = useAsync(
    (signal) => librariesApi.facets(library.id, field, "", signal),
    [library.id, field],
  );
  const label =
    library.kind?.fields?.find((f) => f.key === field)?.label ??
    (field === "year"
      ? t("Year")
      : field === "rating"
        ? t("Rating")
        : field === "released"
          ? t("Recently released")
          : field);
  const options = [
    { value: "", label: t("Any {field}", { field: label.toLowerCase() }) },
    ...(facets.data?.values ?? []).map((v) => ({
      value: v.value ?? v.title ?? "",
      label: `${v.value ?? v.title ?? ""} (${v.count})`,
    })),
  ];
  return <ToolbarPicker ariaLabel={label} value={value} options={options} onChange={onChange} />;
}
