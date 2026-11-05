import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Plus, Search, Sparkles, Undo2, X, Bookmark, ChevronDown } from "lucide-react";
import { useRef, useState } from "react";
import { t } from "../../i18n";
import { useNavigate } from "react-router-dom";
import { newCollectionHref } from "../collections/links";
import { collectionDraft } from "./catalogSavedView";
import { catalogValueLabel } from "./catalogValueLabel";
import { api, apiFetch } from "../../lib/api";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import {
  compileQuery,
  emptyQuery,
  parseNatural,
  readQuery,
  type Chip,
  type QueryState,
} from "./catalogQuery";

type Field = {
  key: string;
  label: string;
  category: string;
  kind?: "number" | "date" | "person";
  values?: string[];
  facet?: "genre" | "language" | "country" | "contentRating" | "studio" | "decade" | "tag";
};
const fields = (): Field[] => [
  {
    key: "playable",
    label: t("Playable now"),
    category: t("Availability"),
    values: ["yes", "unknown"],
  },
  {
    key: "cached",
    label: t("Cached on debrid"),
    category: t("Availability"),
    values: ["yes", "unknown"],
  },
  {
    key: "quality",
    label: t("Quality"),
    category: t("Availability"),
    values: ["4k", "hdr", "dv", "atmos"],
  },
  { key: "title", label: t("Title"), category: t("Essentials") },
  { key: "type", label: t("Type"), category: t("Essentials"), values: ["movie", "show"] },
  { key: "tag", label: t("Tags"), category: t("People & stories"), facet: "tag" },
  {
    key: "anime",
    label: t("Anime keyword"),
    category: t("Origin & classification"),
    values: ["true"],
  },
  { key: "kids", label: t("Kids genre"), category: t("Origin & classification"), values: ["true"] },
  { key: "genre", label: t("Genre"), category: t("Essentials"), facet: "genre" },
  { key: "decade", label: t("Decade"), category: t("Essentials"), facet: "decade", kind: "number" },
  { key: "year", label: t("Year"), category: t("Dates"), kind: "number" },
  { key: "released", label: t("Release / air date"), category: t("Dates"), kind: "date" },
  { key: "rating", label: t("TMDB rating"), category: t("Ratings & runtime"), kind: "number" },
  { key: "votes", label: t("TMDB votes"), category: t("Ratings & runtime"), kind: "number" },
  { key: "popularity", label: t("Popularity"), category: t("Ratings & runtime"), kind: "number" },
  {
    key: "runtime",
    label: t("Runtime (minutes)"),
    category: t("Ratings & runtime"),
    kind: "number",
  },
  {
    key: "language",
    label: t("Original language"),
    category: t("Origin & classification"),
    facet: "language",
  },
  { key: "country", label: t("Country"), category: t("Origin & classification"), facet: "country" },
  {
    key: "certification",
    label: t("Certification"),
    category: t("Origin & classification"),
    facet: "contentRating",
  },
  { key: "studio", label: t("Studio / network"), category: t("People & stories"), facet: "studio" },
  { key: "keyword", label: t("Keyword"), category: t("People & stories") },
  { key: "cast", label: t("Actor"), category: t("People & stories"), kind: "person" },
  { key: "director", label: t("Director"), category: t("People & stories"), kind: "person" },
  { key: "writer", label: t("Writer"), category: t("People & stories"), kind: "person" },
  { key: "composer", label: t("Composer"), category: t("People & stories"), kind: "person" },
  {
    key: "collection",
    label: t("Collection / franchise"),
    category: t("People & stories"),
    kind: "number",
  },
  {
    key: "watched",
    label: t("Watch status"),
    category: t("Viewer activity"),
    values: ["unwatched", "watched"],
  },
  { key: "rated", label: t("Rated"), category: t("Viewer activity"), values: ["true", "false"] },
  {
    key: "watchlist",
    label: t("On watchlist"),
    category: t("Viewer activity"),
    values: ["true", "false"],
  },
  { key: "etherAdded", label: t("Added to Ether"), category: t("Dates"), kind: "date" },
  {
    key: "progress",
    label: t("In progress"),
    category: t("Viewer activity"),
    values: ["true", "false"],
  },
  {
    key: "status",
    label: t("Show status"),
    category: t("Essentials"),
    values: ["Returning Series", "Ended", "Canceled", "In Production"],
  },
  { key: "seasons", label: t("Seasons"), category: t("Ratings & runtime"), kind: "number" },
  { key: "episodes", label: t("Episodes"), category: t("Ratings & runtime"), kind: "number" },
];
export function CatalogFilterBar({
  params,
  onChange,
  viewers,
  collection = false,
}: {
  params: URLSearchParams;
  onChange: (params: URLSearchParams) => void;
  viewers: { id: number; title?: string; username?: string }[];
  collection?: boolean;
}) {
  const navigate = useNavigate();
  const naturalRef = useRef<HTMLDetailsElement>(null);
  const savedRef = useRef<HTMLDetailsElement>(null);
  const closeMenus = () => {
    if (naturalRef.current) naturalRef.current.open = false;
    if (savedRef.current) savedRef.current.open = false;
  };
  const raw = params.get("filters");
  const state = readQuery(raw);
  const [undo, setUndo] = useState<string | null>(null);
  const [picker, setPicker] = useState(false);
  const [pickerSearch, setPickerSearch] = useState("");
  const [editing, setEditing] = useState<number | null>(null);
  const [draft, setDraft] = useState<Chip>({ field: "genre", op: "is", value: "" });
  const [natural, setNatural] = useState("");
  const [saved, setSaved] = useState<{ name: string; query: string }[]>(() => {
    try {
      const value = JSON.parse(localStorage.getItem("ether.catalog.views") || "[]");
      return Array.isArray(value)
        ? value
            .filter((v) => v && typeof v.name === "string" && typeof v.query === "string")
            .slice(0, 30)
        : [];
    } catch {
      return [];
    }
  });
  const [interpreting, setInterpreting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const definitions = fields();
  const field = definitions.find((f) => f.key === draft.field)!;
  const search = useDebouncedValue(draft.label || draft.value, 250);
  const suggestions = useAsync(
    async (signal) => {
      if (editing === null) return [];
      if (field.kind === "person")
        return search.length >= 2
          ? (await api.lookup("person", search, undefined, signal)).map((v) => ({
              key: String(v.id),
              title: v.name,
            }))
          : [];
      if (field.key === "keyword")
        return search.length >= 2
          ? (await api.lookup("keyword", search, undefined, signal)).map((v) => ({
              key: String(v.id),
              title: v.name,
            }))
          : [];
      if (field.key === "collection")
        return search.length >= 2
          ? (await api.lookup("collection", search, undefined, signal)).map((v) => ({
              key: String(v.id),
              title: v.name,
            }))
          : [];
      if (field.facet) {
        const p = new URLSearchParams({
          type: params.get("type") || "",
          facet: field.facet,
          deferred: "1",
        });
        for (let attempt = 0; attempt < 20; attempt++) {
          const response = await apiFetch<{
            values: { key: string; title: string; count?: number }[];
            pending: boolean;
          }>(`/catalog/facets?${p}`, { signal });
          if (!response.pending) return response.values || [];
          await new Promise<void>((resolve, reject) => {
            const stop = () => {
              clearTimeout(timer);
              reject(new DOMException("Aborted", "AbortError"));
            };
            const timer = setTimeout(() => {
              signal?.removeEventListener("abort", stop);
              resolve();
            }, 500);
            signal?.addEventListener("abort", stop, { once: true });
          });
        }
        return [];
      }
      return [];
    },
    [
      editing,
      field.key,
      field.kind === "person" || ["keyword", "collection"].includes(field.key) ? search : "",
      params.get("type"),
    ],
  );
  function commit(next: QueryState) {
    setUndo(params.toString());
    const p = new URLSearchParams(params);
    if (next.chips.length) p.set("filters", JSON.stringify(next));
    else p.delete("filters");
    p.delete("page");
    onChange(p);
  }
  function edit(index: number, chip?: Chip) {
    closeMenus();
    setEditing(index);
    setDraft(chip || { field: "genre", op: "is", value: "" });
    setPicker(false);
  }
  function add() {
    if (!draft.value.trim()) return;
    const chips = [...state.chips];
    chips[editing ?? chips.length] = { ...draft, value: draft.value.trim() };
    commit({ ...state, chips });
    setEditing(null);
  }
  const valid =
    Boolean(draft.value.trim()) &&
    (!(field.kind === "number") ||
      (Number.isFinite(Number(draft.value)) && Number(draft.value) >= 0));
  const legacy = ["q", "genre", "decade", "waiting"].filter((k) => params.get(k));
  const hasActivity = state.chips.some((c) =>
    ["watched", "progress", "rated", "watchlist"].includes(c.field),
  );
  return (
    <section
      className="catalog-query"
      aria-label={t("Catalog filters")}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          closeMenus();
          setEditing(null);
          setPicker(false);
        }
      }}
    >
      <div className="catalog-query-search">
        <Search size={17} aria-hidden />
        <Input
          aria-label={t("Find a title")}
          placeholder={t("Search catalog…")}
          value={params.get("q") || ""}
          onChange={(e) => {
            const p = new URLSearchParams(params);
            p.delete("page");
            e.target.value ? p.set("q", e.target.value) : p.delete("q");
            onChange(p);
          }}
        />
        <details ref={naturalRef} className="catalog-natural">
          <summary>
            <Sparkles size={14} />
            {t("Describe what you want")}
          </summary>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              closeMenus();
              setInterpreting(true);
              let chips: Chip[];
              try {
                const result = await apiFetch<{ chips: Chip[] }>("/catalog/interpret", {
                  method: "POST",
                  body: JSON.stringify({ text: natural }),
                });
                chips = readQuery(JSON.stringify({ mode: "and", chips: result.chips })).chips;
              } catch {
                chips = parseNatural(natural);
              } finally {
                setInterpreting(false);
              }
              commit({ ...state, chips: [...state.chips, ...chips] });
              setNatural("");
            }}
          >
            <label htmlFor="catalog-natural">{t("Turn a description into editable filters")}</label>
            <Input
              id="catalog-natural"
              value={natural}
              onChange={(e) => setNatural(e.target.value)}
              placeholder={t("80s comedies over 7 with Tom Hanks under 2h")}
            />
            <Button size="sm" disabled={!natural.trim() || interpreting}>
              {t("Create filters")}
            </Button>
            <p>
              {t("Review the chips before exploring. Unrecognized words become title searches.")}
            </p>
          </form>
        </details>
      </div>
      <div className="catalog-query-chips">
        {state.chips.length > 1 ? (
          <select
            aria-label={t("Match filters")}
            value={state.mode}
            onChange={(e) => commit({ ...state, mode: e.target.value as "and" | "or" })}
          >
            <option value="and">{t("Match all")}</option>
            <option value="or">{t("Match any")}</option>
          </select>
        ) : null}
        {state.chips.some((c) => c.group) ? (
          <select
            aria-label={t("Match within groups")}
            value={state.groupMode || "and"}
            onChange={(e) => commit({ ...state, groupMode: e.target.value as "and" | "or" })}
          >
            <option value="and">{t("All in each group")}</option>
            <option value="or">{t("Any in each group")}</option>
          </select>
        ) : null}
        {state.chips.map((chip, index) => (
          <div className="catalog-query-chip" key={index}>
            <button
              onClick={() => edit(index, chip)}
              aria-label={t("Edit {filter} filter", {
                filter: definitions.find((f) => f.key === chip.field)?.label || chip.field,
              })}
            >
              {chip.group ? (
                <span className="catalog-query-group">
                  {t("Group")} {chip.group}
                </span>
              ) : null}
              <span>{definitions.find((f) => f.key === chip.field)?.label || chip.field}</span>
              <span className="catalog-query-op">
                {chip.op === "min" ? "≥" : chip.op === "max" ? "≤" : "="}
              </span>
              <strong>{catalogValueLabel(chip.field, chip.label || chip.value)}</strong>
            </button>
            <button
              aria-label={t("Remove {filter} filter", {
                filter: catalogValueLabel(chip.field, chip.label || chip.value),
              })}
              onClick={() => commit({ ...state, chips: state.chips.filter((_, i) => i !== index) })}
            >
              <X size={13} />
            </button>
          </div>
        ))}
        {legacy.map((key) => (
          <button
            key={key}
            aria-label={t("Remove {filter} filter", {
              filter: key === "waiting" ? t("Waiting for a release") : params.get(key) || "",
            })}
            className="catalog-query-chip"
            onClick={() => {
              setUndo(params.toString());
              const p = new URLSearchParams(params);
              p.delete(key);
              p.delete("page");
              onChange(p);
            }}
          >
            {key === "waiting" ? t("Waiting for a release") : params.get(key)}
            <X size={13} />
          </button>
        ))}
        {!collection ? (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              closeMenus();
              setPicker(!picker);
              setEditing(null);
            }}
            aria-expanded={picker}
          >
            <Plus size={14} />
            {t("Filter")}
          </Button>
        ) : null}
        {state.chips.length || legacy.length ? (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setUndo(params.toString());
              const p = new URLSearchParams(params);
              ["filters", "q", "genre", "decade", "waiting", "page"].forEach((k) => p.delete(k));
              onChange(p);
            }}
          >
            {t("Clear filters")}
          </Button>
        ) : null}
        {undo !== null ? (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              onChange(new URLSearchParams(undo));
              setUndo(null);
            }}
          >
            <Undo2 size={13} />
            {t("Undo")}
          </Button>
        ) : null}
        <div className="catalog-query-spacer" />
        <details ref={savedRef} className="catalog-saved">
          <summary>
            <Bookmark size={13} />
            {t("Saved views")}
            <ChevronDown size={12} />
          </summary>
          <div>
            {saved.map((view, index) => (
              <div className="catalog-saved-row" key={index}>
                <button
                  onClick={() => {
                    setUndo(params.toString());
                    onChange(new URLSearchParams(view.query));
                  }}
                >
                  {view.name}
                </button>
                {collectionDraft(view.name, new URLSearchParams(view.query)) ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      const spec = collectionDraft(view.name, new URLSearchParams(view.query));
                      if (spec) navigate(newCollectionHref(), { state: { spec } });
                    }}
                  >
                    {t("Create collection")}
                  </Button>
                ) : null}
                <button
                  aria-label={t("Delete {name}", { name: view.name })}
                  onClick={() => {
                    const next = saved.filter((_, i) => i !== index);
                    setSaved(next);
                    localStorage.setItem("ether.catalog.views", JSON.stringify(next));
                  }}
                >
                  <X size={12} />
                </button>
              </div>
            ))}
            {saving ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const next = [
                    ...saved.filter((v) => v.name !== name.trim()),
                    { name: name.trim(), query: params.toString() },
                  ].slice(-30);
                  setSaved(next);
                  localStorage.setItem("ether.catalog.views", JSON.stringify(next));
                  setSaving(false);
                  setName("");
                }}
              >
                <Input
                  aria-label={t("View name")}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  autoFocus
                />
                <Button size="sm" disabled={!name.trim()}>
                  {t("Save")}
                </Button>
              </form>
            ) : (
              <Button size="sm" variant="ghost" onClick={() => setSaving(true)}>
                {t("Save current view")}
              </Button>
            )}
            <p>{t("Saved on this browser. Copy the URL to share a view.")}</p>
          </div>
        </details>
      </div>
      {picker ? (
        <div className="catalog-filter-picker">
          <Input
            autoFocus
            aria-label={t("Find a filter")}
            placeholder={t("Find a filter")}
            value={pickerSearch}
            onChange={(e) => setPickerSearch(e.target.value)}
          />
          <div className="catalog-filter-categories">
            {[...new Set(definitions.map((f) => f.category))].map((category) => (
              <section key={category}>
                <h4>{category}</h4>
                {definitions
                  .filter(
                    (f) =>
                      f.category === category &&
                      f.label.toLowerCase().includes(pickerSearch.toLowerCase()),
                  )
                  .map((f) => (
                    <button
                      key={f.key}
                      onClick={() =>
                        edit(state.chips.length, {
                          field: f.key,
                          op: "is",
                          value: f.values?.[0] || "",
                        })
                      }
                    >
                      {f.label}
                      <Plus size={12} />
                    </button>
                  ))}
              </section>
            ))}
          </div>
        </div>
      ) : null}
      {editing !== null ? (
        <form
          className="catalog-chip-editor"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) add();
          }}
        >
          <label>
            {t("Field")}
            <select
              value={draft.field}
              onChange={(e) =>
                setDraft({ ...draft, field: e.target.value, value: "", label: undefined })
              }
            >
              {definitions.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t("Operator")}
            <select value={draft.op} onChange={(e) => setDraft({ ...draft, op: e.target.value })}>
              <option value="is">{t("is")}</option>
              {field.kind === "number" || field.kind === "date" ? (
                <>
                  <option value="min">{t("at least / after")}</option>
                  <option value="max">{t("at most / before")}</option>
                </>
              ) : null}
            </select>
          </label>
          <label className="catalog-chip-value">
            {t("Value")}
            {field.values ? (
              <select
                value={draft.value}
                onChange={(e) => setDraft({ ...draft, value: e.target.value })}
              >
                {field.values.map((v) => (
                  <option key={v} value={v}>
                    {catalogValueLabel(field.key, v)}
                  </option>
                ))}
              </select>
            ) : (
              <Input
                autoFocus
                type={
                  field.kind === "date"
                    ? "date"
                    : field.kind === "number" && field.key !== "collection"
                      ? "number"
                      : "text"
                }
                min={0}
                step="any"
                value={draft.label || draft.value}
                onChange={(e) => setDraft({ ...draft, value: e.target.value, label: undefined })}
              />
            )}
          </label>
          <label>
            {t("Group")}
            <select
              value={draft.group || 0}
              onChange={(e) => setDraft({ ...draft, group: Number(e.target.value) })}
            >
              {[0, 1, 2, 3].map((n) => (
                <option key={n} value={n}>
                  {n || t("Default")}
                </option>
              ))}
            </select>
          </label>
          <Button size="sm" disabled={!valid}>
            {t("Apply")}
          </Button>
          <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(null)}>
            {t("Cancel")}
          </Button>
          {suggestions.data?.length ? (
            <div className="catalog-value-suggestions">
              {suggestions.data
                .filter((item) => item.title.toLowerCase().includes(search.toLowerCase()))
                .slice(0, 16)
                .map((item, index) => {
                  const value = String(item.key);
                  return (
                    <button
                      type="button"
                      key={index}
                      onClick={() =>
                        setDraft({
                          ...draft,
                          value:
                            field.facet || field.kind === "person" || field.key === "collection"
                              ? value
                              : item.title,
                          label: item.title,
                        })
                      }
                    >
                      {item.title}
                      {"count" in item ? <small>{String(item.count)}</small> : null}
                    </button>
                  );
                })}
            </div>
          ) : null}
        </form>
      ) : null}
      {hasActivity ? (
        <label className="catalog-viewer-filter">
          {t("Viewer")}
          <select
            aria-label={t("Viewer")}
            value={params.get("viewer") || ""}
            onChange={(e) => {
              const p = new URLSearchParams(params);
              p.set("viewer", e.target.value);
              p.delete("page");
              onChange(p);
            }}
          >
            <option value="">{t("Choose a viewer")}</option>
            {viewers.map((v) => (
              <option key={v.id} value={v.id}>
                {v.title || v.username}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {state.chips.some((c) => ["released", "etherAdded"].includes(c.field)) ? (
        <p className="catalog-query-note">{t("Date added follows the release or air date.")}</p>
      ) : null}
    </section>
  );
}
export { compileQuery, readQuery, emptyQuery };
