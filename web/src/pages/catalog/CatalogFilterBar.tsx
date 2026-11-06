import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Plus, Search, Sparkles, Undo2, X } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { catalogValueLabel } from "./catalogValueLabel";
import { api, apiFetch, ApiError } from "../../lib/api";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import {
  compileQuery,
  emptyQuery,
  readAIQuery,
  mergeAIQuery,
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
    key: "gate",
    label: t("New-release gate"),
    category: t("Availability"),
    values: ["admitted", "held", "unknown"],
  },
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
  { key: "dateAdded", label: t("Date added"), category: t("Dates"), kind: "date" },
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
  leading,
  actions,
  emptySearch,
}: {
  params: URLSearchParams;
  onChange: (params: URLSearchParams) => void;
  viewers: { id: number; title?: string; username?: string }[];
  collection?: boolean;
  leading?: ReactNode;
  actions?: ReactNode;
  /** Exact query whose completed regular search returned no titles. */
  emptySearch?: string;
}) {
  const latestParams = useRef(params);
  useLayoutEffect(() => {
    latestParams.current = params;
  }, [params]);
  const publish = (next: URLSearchParams) => {
    latestParams.current = next;
    onChange(next);
  };
  const raw = params.get("filters");
  const state = readQuery(raw);
  const [undo, setUndo] = useState<string | null>(null);
  const [picker, setPicker] = useState(false);
  const [pickerSearch, setPickerSearch] = useState("");
  const [editing, setEditing] = useState<number | null>(null);
  const [draft, setDraft] = useState<Chip>({ field: "genre", op: "is", value: "" });
  const [interpreting, setInterpreting] = useState(false);
  const [aiError, setAiError] = useState(false);
  const ai = useAsync(
    (signal) => apiFetch<{ enabled: boolean }>("/catalog/interpret", { signal }),
    [],
  );
  const pendingInterpret = useRef<AbortController | null>(null);
  const lastInterpreted = useRef("");
  const phrase = useDebouncedValue(params.get("q") || "", 750);
  async function interpret(text: string) {
    if (!ai.data?.enabled || !text.trim()) return;
    pendingInterpret.current?.abort();
    const controller = new AbortController();
    pendingInterpret.current = controller;
    lastInterpreted.current = text;
    setInterpreting(true);
    setAiError(false);
    try {
      // A cold vocabulary is temporary and does not call the model. Keep the
      // same user action pending while its background snapshot completes.
      const deadline = Date.now() + 12_000;
      let result: unknown;
      for (;;) {
        try {
          result = await apiFetch<unknown>("/catalog/interpret", {
            method: "POST",
            body: JSON.stringify({ text }),
            signal: controller.signal,
          });
          break;
        } catch (error) {
          if (
            !(error instanceof ApiError) ||
            error.code !== "vocabulary_unavailable" ||
            error.status !== 503 ||
            Date.now() >= deadline ||
            controller.signal.aborted
          )
            throw error;
          await new Promise<void>((resolve, reject) => {
            const stop = () => {
              clearTimeout(timer);
              reject(new DOMException("Aborted", "AbortError"));
            };
            const timer = setTimeout(() => {
              controller.signal.removeEventListener("abort", stop);
              resolve();
            }, 300);
            controller.signal.addEventListener("abort", stop, { once: true });
          });
        }
      }
      if (controller.signal.aborted || latestParams.current.get("q") !== text) return;
      const draft = readAIQuery(result);
      const next = new URLSearchParams(latestParams.current);
      const current = readQuery(next.get("filters"));
      const merged = mergeAIQuery(current, draft);
      setUndo(next.toString());
      next.set("filters", JSON.stringify(merged));
      next.delete("q");
      next.delete("page");
      publish(next);
    } catch {
      if (!controller.signal.aborted) setAiError(true);
    } finally {
      if (pendingInterpret.current === controller) setInterpreting(false);
    }
  }
  useEffect(() => {
    if (
      ai.data?.enabled &&
      (looksLikeCatalogRequest(phrase) || (!!phrase.trim() && emptySearch === phrase)) &&
      lastInterpreted.current !== phrase &&
      latestParams.current.get("q") === phrase
    )
      void interpret(phrase);
  }, [phrase, ai.data?.enabled, emptySearch]);
  useEffect(() => () => pendingInterpret.current?.abort(), []);
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
  function commit(update: QueryState | ((current: QueryState) => QueryState)) {
    const current = readQuery(latestParams.current.get("filters"));
    const next = typeof update === "function" ? update(current) : update;
    setUndo(latestParams.current.toString());
    const p = new URLSearchParams(latestParams.current);
    if (next.chips.length) p.set("filters", JSON.stringify(next));
    else p.delete("filters");
    p.delete("page");
    publish(p);
  }
  function edit(index: number, chip?: Chip) {
    setEditing(index);
    setDraft(chip || { field: "genre", op: "is", value: "" });
    setPicker(false);
  }
  function add() {
    if (!draft.value.trim()) return;
    commit((current) => {
      const chips = [...current.chips];
      chips[editing ?? chips.length] = { ...draft, value: draft.value.trim() };
      return { ...current, chips };
    });
    setEditing(null);
  }
  const valid =
    Boolean(draft.value.trim()) &&
    (!(field.kind === "number") ||
      (Number.isFinite(Number(draft.value)) && Number(draft.value) >= 0));
  const legacy = ["genre", "decade", "waiting"].filter((k) => params.get(k));
  const hasActivity = state.chips.some((c) =>
    ["watched", "progress", "rated", "watchlist"].includes(c.field),
  );
  return (
    <section
      className="catalog-query"
      aria-label={t("Catalog filters")}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          setEditing(null);
          setPicker(false);
        }
      }}
    >
      <div className="catalog-query-search">
        {leading}
        <form
          className="catalog-unified-search"
          onSubmit={(e) => {
            e.preventDefault();
            const text = latestParams.current.get("q") || "";
            if (ai.data?.enabled && looksLikeCatalogRequest(text)) void interpret(text);
          }}
        >
          <Search size={16} aria-hidden />
          <Input
            aria-label={t("Find a title")}
            maxLength={1000}
            placeholder={
              ai.data?.enabled
                ? t("Search titles or describe what you want…")
                : t("Search catalog…")
            }
            value={params.get("q") || ""}
            onChange={(e) => {
              pendingInterpret.current?.abort();
              setInterpreting(false);
              setAiError(false);
              lastInterpreted.current = "";
              const p = new URLSearchParams(latestParams.current);
              p.delete("page");
              e.target.value ? p.set("q", e.target.value) : p.delete("q");
              publish(p);
            }}
          />
          {params.get("q") || "" ? (
            <Button
              type="button"
              size="icon"
              variant="ghost"
              aria-label={t("Clear search")}
              onClick={() => {
                pendingInterpret.current?.abort();
                setInterpreting(false);
                setAiError(false);
                setUndo(latestParams.current.toString());
                const next = new URLSearchParams(latestParams.current);
                next.delete("q");
                next.delete("page");
                publish(next);
              }}
            >
              <X size={14} />
            </Button>
          ) : null}
          {ai.data?.enabled ? (
            <Button
              size="icon"
              type="button"
              variant="ghost"
              aria-label={t("Turn search into filters")}
              disabled={interpreting || !(params.get("q") || "").trim()}
              onClick={() => void interpret(latestParams.current.get("q") || "")}
            >
              <Sparkles size={15} className={interpreting ? "animate-pulse" : ""} />
            </Button>
          ) : null}
        </form>
        {!collection ? (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setPicker(!picker);
              setEditing(null);
            }}
            aria-expanded={picker}
          >
            <Plus size={14} />
            {t("Filter")}
          </Button>
        ) : null}
        {actions ? <div className="catalog-header-actions">{actions}</div> : null}
      </div>
      {aiError || ai.error ? (
        <p className="catalog-ai-error" role="alert">
          {t(
            "AI search is unavailable or could not interpret this request. Try again or use filters.",
          )}
        </p>
      ) : null}
      {state.chips.length || legacy.length || undo !== null ? (
        <div className="catalog-query-chips">
          {state.chips.length > 1 ? (
            <select
              aria-label={t("Match filters")}
              value={state.mode}
              onChange={(e) =>
                commit((current) => ({ ...current, mode: e.target.value as "and" | "or" }))
              }
            >
              <option value="and">{t("Match all")}</option>
              <option value="or">{t("Match any")}</option>
            </select>
          ) : null}
          {state.chips.some((c) => c.group) ? (
            <select
              aria-label={t("Match within groups")}
              value={state.groupMode || "and"}
              onChange={(e) =>
                commit((current) => ({ ...current, groupMode: e.target.value as "and" | "or" }))
              }
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
                onClick={() =>
                  commit((current) => ({
                    ...current,
                    chips: current.chips.filter((_, i) => i !== index),
                  }))
                }
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
                setUndo(latestParams.current.toString());
                const p = new URLSearchParams(latestParams.current);
                p.delete(key);
                p.delete("page");
                publish(p);
              }}
            >
              {key === "waiting" ? t("Waiting for a release") : params.get(key)}
              <X size={13} />
            </button>
          ))}
          {state.chips.length || legacy.length ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setUndo(latestParams.current.toString());
                const p = new URLSearchParams(latestParams.current);
                ["filters", "q", "genre", "decade", "waiting", "page"].forEach((k) => p.delete(k));
                publish(p);
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
                publish(new URLSearchParams(undo));
                setUndo(null);
              }}
            >
              <Undo2 size={13} />
              {t("Undo")}
            </Button>
          ) : null}
        </div>
      ) : null}
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
              const p = new URLSearchParams(latestParams.current);
              p.set("viewer", e.target.value);
              p.delete("page");
              publish(p);
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
    </section>
  );
}
export { compileQuery, readQuery, emptyQuery };

// This only routes clearly descriptive input to the configured model; it never
// invents predicates locally. Literal title searches stay literal.
export function looksLikeCatalogRequest(text: string) {
  return (
    text.trim().split(/\s+/).length >= 4 &&
    (/^(?:find|show me|movies|films|series|shows)\s/i.test(text.trim()) ||
      (
        text.match(
          /\b(?:under|over|above|below|rated|unwatched|watched|starring|with|before|after|\d{2}s|playable|comedies|thrillers|hours?)\b/gi,
        ) || []
      ).length >= 2)
  );
}
