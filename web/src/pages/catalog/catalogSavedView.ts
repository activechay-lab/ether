import type { CollectionSpec, Rule, RuleField } from "../../lib/contract";
import { blankSpec } from "../../lib/rules";
import { readQuery } from "./catalogQuery";

/** Lossless predicate promotion only; unsupported views stay browsable and shareable. */
export function collectionDraft(name: string, params: URLSearchParams): CollectionSpec | null {
  if (["q", "waiting", "secondarySort", "viewer"].some((key) => params.get(key))) return null;
  const raw = params.get("filters");
  const state = readQuery(raw);
  // Never convert a corrupt URL after readQuery has discarded a predicate.
  if (raw) {
    try {
      const stored = JSON.parse(raw);
      if (!Array.isArray(stored.chips) || stored.chips.length !== state.chips.length) return null;
    } catch {
      return null;
    }
  }
  if (state.mode !== "and" || state.chips.some((chip) => chip.group)) return null;
  const typeChips = state.chips.filter((chip) => chip.field === "type");
  const types = [params.get("type"), ...typeChips.map((chip) => chip.value)].filter(Boolean);
  if (types.some((type) => type !== "movie" && type !== "show") || new Set(types).size > 1)
    return null;
  const sort = params.get("sort") || "popularity";
  if (!["popularity", "rating", "released", "added", "title"].includes(sort)) return null;
  const spec = blankSpec((types[0] as "movie" | "show" | undefined) ?? "mixed");
  spec.name = name;
  spec.refresh = "live";
  spec.sort = sort as CollectionSpec["sort"];
  spec.sortDesc = params.has("desc") ? params.get("desc") !== "0" : sort !== "title";
  const rules: Rule[] = [];
  if (params.get("genre")) rules.push({ field: "genre", op: "is", value: params.get("genre") });
  if (params.get("decade"))
    rules.push({ field: "decade", op: "is", value: Number(params.get("decade")) });
  for (const chip of state.chips) {
    if (chip.field === "type") {
      if (chip.op !== "is") return null;
      continue;
    }
    const numeric = ["year", "decade", "rating", "votes", "popularity", "runtime"].includes(
      chip.field,
    );
    const tags = ["genre", "language", "country"].includes(chip.field);
    if (!numeric && !tags) return null;
    if (numeric && (!Number.isFinite(Number(chip.value)) || Number(chip.value) <= 0)) return null;
    if (chip.field === "decade" && chip.op !== "is") return null;
    if (tags && chip.op !== "is") return null;
    rules.push({
      field: chip.field as RuleField,
      op: chip.op === "min" ? "gte" : chip.op === "max" ? "lte" : "is",
      value: numeric ? Number(chip.value) : chip.value,
      label: chip.label,
    });
  }
  spec.rules = rules;
  return spec;
}
