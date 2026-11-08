import {
  Badge,
  Button,
  Input,
  SearchSelect,
  Select,
  Switch,
  Textarea,
} from "@braedonsaunders/appkit-ui";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  Plus,
  Puzzle,
  Trash2,
  Upload,
  Download,
} from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { DragHandle, type DragHandleProps } from "../../components/Sortable";
import { t, getLocale } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { apiFetch, errorMessage, isAbort, uuid } from "../../lib/api";
import {
  isPluginDescriptor,
  textOf,
  useRegistry,
  type Descriptor,
  type ExtendText,
} from "../../lib/extend";
import { withRuleScores } from "../../lib/qualityRanking";
import type { QualityProfile, ReleaseCondition, ReleaseRule } from "../../lib/types";
import type { ConsoleRelease } from "../../lib/releases";
import { TitlePicker } from "./Search";

/**
 * Ranking step and rule field descriptor metadata, mirroring
 * internal/extend/meta_ranking.go. The server registry is the one source
 * for which steps and fields exist, their order, editors and choices;
 * this file keeps no copy of those lists.
 */
interface DescriptorOption {
  value: string;
  label?: ExtendText;
}

type RankingEditorKind = "ordered_list" | "choice" | "range" | "toggle" | "none";

interface RankingStepMeta {
  editor:
    | RankingEditorKind
    | {
        kind?: RankingEditorKind;
        editor?: RankingEditorKind;
        options?: (DescriptorOption | string)[];
        choices?: (DescriptorOption | string)[];
      };
  options?: DescriptorOption[];
  choices?: DescriptorOption[];
  required?: boolean;
  canMove: boolean;
  defaultPosition: number;
  configKey?: string;
  hint?: string;
}

type RuleValueClass = "text" | "choice" | "language" | "number" | "none";

interface RuleFieldMeta {
  valueClass: RuleValueClass;
  operators?: string[];
  defaultOperator?: string;
  choices?: DescriptorOption[];
  unit?: string;
  singleValue?: boolean;
  editor?:
    | string
    | {
        kind?: string;
        valueClass?: string;
        choices?: DescriptorOption[];
        options?: DescriptorOption[];
        unit?: string;
        singleValue?: boolean;
      };
}

export type RankingStepDescriptor = Descriptor<RankingStepMeta>;
export type RuleFieldDescriptor = Descriptor<RuleFieldMeta>;

function normalizeValueClass(raw: string | undefined): RuleValueClass | undefined {
  if (!raw) return undefined;
  if (raw === "range") return "number";
  if (
    raw === "text" ||
    raw === "choice" ||
    raw === "language" ||
    raw === "number" ||
    raw === "none"
  ) {
    return raw;
  }
  return undefined;
}

/**
 * Normalizes a ruleField descriptor's meta whether it was registered directly
 * as RuleFieldMeta or built from a plugin manifest's ruleConditions entry.
 */
export function resolveRuleFieldMeta(meta: RuleFieldMeta | undefined):
  | {
      valueClass?: RuleValueClass;
      operators?: string[];
      defaultOperator?: string;
      choices?: DescriptorOption[];
      unit?: string;
      singleValue?: boolean;
    }
  | undefined {
  if (!meta) return undefined;
  let valueClass = normalizeValueClass(meta.valueClass);
  let choices = meta.choices;
  let unit = meta.unit;
  let singleValue = meta.singleValue;
  if (typeof meta.editor === "string") {
    valueClass = valueClass ?? normalizeValueClass(meta.editor);
  } else if (typeof meta.editor === "object" && meta.editor !== null) {
    valueClass = valueClass ?? normalizeValueClass(meta.editor.valueClass ?? meta.editor.kind);
    if (!choices?.length) {
      choices = meta.editor.choices ?? meta.editor.options;
    }
    unit = unit ?? meta.editor.unit;
    singleValue = singleValue ?? meta.editor.singleValue;
  }
  const operators = meta.operators?.length ? meta.operators : undefined;
  if (!valueClass && operators?.length === 1 && operators[0] === "range") {
    valueClass = "number";
  }
  const defaultOperator = meta.defaultOperator || operators?.[0];
  return {
    valueClass,
    operators,
    defaultOperator,
    choices,
    unit,
    singleValue,
  };
}

/** Ranking steps and rule fields from the descriptor registry, in registry order. */
export function useRankingRegistry(): {
  steps: RankingStepDescriptor[];
  fields: RuleFieldDescriptor[];
  loading: boolean;
  error: Error | null;
  reload: () => void;
} {
  const { descriptors, loading, error, reload } = useRegistry("rankingStep", "ruleField");
  return useMemo(
    () => ({
      steps: descriptors.filter((d) => d.kind === "rankingStep") as RankingStepDescriptor[],
      fields: descriptors.filter((d) => d.kind === "ruleField") as RuleFieldDescriptor[],
      loading,
      error,
      reload,
    }),
    [descriptors, loading, error, reload],
  );
}

/** Display name for a plugin-contributed descriptor's owner. */
export function descriptorOwnerName(d: Descriptor): string {
  return d.group || d.owner;
}

/** Small badge marking a descriptor a plugin contributed. */
export function PluginBadge({ owner }: { owner: string }) {
  return (
    <Badge variant="secondary" className="inline-flex items-center gap-1">
      <Puzzle size={11} aria-hidden />
      {t("From {name}", { name: owner })}
    </Badge>
  );
}

export function descriptorLabel(d: Descriptor): string {
  const label = textOf(d.label) || d.id;
  return isPluginDescriptor(d)
    ? `${label} · ${t("From {name}", { name: descriptorOwnerName(d) })}`
    : label;
}

function optionLabel(option: DescriptorOption): string {
  return textOf(option.label) || option.value;
}

function fieldDescriptor(
  fields: RuleFieldDescriptor[],
  field: string,
): RuleFieldDescriptor | undefined {
  return fields.find((f) => f.id === field);
}

function fieldLabel(field: string, fields: RuleFieldDescriptor[]) {
  const d = fieldDescriptor(fields, field);
  return d ? textOf(d.label) || field : field;
}

function fieldOptions(fields: RuleFieldDescriptor[], current?: string) {
  const options = fields.map((d) => ({ value: d.id, label: descriptorLabel(d) }));
  if (current && !options.some((o) => o.value === current)) {
    options.unshift({ value: current, label: current });
  }
  return options;
}

function mentionsOriginal(rule: ReleaseRule) {
  return [rule.conditions, rule.unless ?? [], rule.fallback ?? []].some((group) =>
    group.some((c) => c.field === "audio_matches_original"),
  );
}

function languageSuggestions(codes: string[]) {
  const names = new Intl.DisplayNames([getLocale()], { type: "language" });
  return codes
    .map((value) => ({ value, label: names.of(value) ?? value }))
    .sort((a, b) => a.label.localeCompare(b.label, getLocale()));
}

function suggestionsFor(d: RuleFieldDescriptor | undefined) {
  const meta = resolveRuleFieldMeta(d?.meta);
  if (!meta) return [];
  if (meta.valueClass === "language") {
    return languageSuggestions((meta.choices ?? []).map((c) => c.value));
  }
  return (meta.choices ?? []).map((c) => ({ value: c.value, label: optionLabel(c) }));
}

export function newCondition(fields: RuleFieldDescriptor[], field = "name"): ReleaseCondition {
  const meta = resolveRuleFieldMeta(fieldDescriptor(fields, field)?.meta);
  return {
    field,
    operator: (meta?.defaultOperator ??
      meta?.operators?.[0] ??
      "equals") as ReleaseCondition["operator"],
    value: "",
  };
}

function retarget(
  current: ReleaseCondition,
  field: string,
  fields: RuleFieldDescriptor[],
): ReleaseCondition {
  const next = newCondition(fields, field);
  if (next.operator !== "range" && current.value) next.value = current.value;
  return next;
}

export function createRule(
  name: string,
  c: ReleaseCondition,
  action: ReleaseRule["action"] = "prefer",
): ReleaseRule {
  return { id: uuid(), name, action, match: "all", conditions: [c] };
}

/** Same conflict the server rejects on save. Exceptions and fallbacks stay allowed. */
export function releaseRuleConflict(rules: ReleaseRule[], fields: RuleFieldDescriptor[]): string {
  const singleValue = new Set(
    fields.filter((f) => resolveRuleFieldMeta(f.meta)?.singleValue).map((f) => f.id),
  );
  const needs: { name: string; field: string; value: string }[] = [];
  for (const r of rules) {
    if (
      r.disabled ||
      r.action !== "require" ||
      r.match === "any" ||
      r.unless?.length ||
      r.fallback?.length
    )
      continue;
    const seen = new Map<string, string>();
    for (const c of r.conditions) {
      if (!singleValue.has(c.field) || c.operator !== "equals") continue;
      const prev = seen.get(c.field);
      if (prev != null && prev.toLowerCase() !== (c.value ?? "").toLowerCase()) {
        return t(
          "Required rule {name} cannot match: {field} would have to equal both {left} and {right}.",
          { name: r.name, field: fieldLabel(c.field, fields), left: prev, right: c.value ?? "" },
        );
      }
      seen.set(c.field, c.value ?? "");
    }
    for (const [field, value] of seen) needs.push({ name: r.name, field, value });
  }
  for (let i = 0; i < needs.length; i++) {
    for (let j = i + 1; j < needs.length; j++) {
      const a = needs[i]!;
      const b = needs[j]!;
      if (a.field === b.field && a.value.toLowerCase() !== b.value.toLowerCase()) {
        return t(
          "Required rules {a} and {b} cannot match the same release: {field} would have to equal both {left} and {right}.",
          {
            a: a.name,
            b: b.name,
            field: fieldLabel(a.field, fields),
            left: a.value,
            right: b.value,
          },
        );
      }
    }
  }
  return "";
}

function ValueField({
  value,
  suggestions,
  mono,
  placeholder,
  onChange,
}: {
  value: string;
  suggestions: { value: string; label: string }[];
  mono?: boolean;
  placeholder?: string;
  onChange: (value: string) => void;
}) {
  const listId = useId();
  return (
    <>
      <Input
        aria-label={t("Value")}
        className={mono ? "font-mono text-xs" : ""}
        value={value}
        placeholder={placeholder}
        list={suggestions.length ? listId : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {suggestions.length > 0 && (
        <datalist id={listId}>
          {suggestions.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </datalist>
      )}
    </>
  );
}

function operatorLabel(operator: string): string {
  switch (operator) {
    case "contains":
      return t("Contains");
    case "regex":
      return t("Regex");
    case "in":
      return t("Is one of");
    default:
      return t("Equals");
  }
}

function ConditionEditor({
  value: c,
  fields,
  onChange,
}: {
  value: ReleaseCondition;
  fields: RuleFieldDescriptor[];
  onChange: (c: ReleaseCondition) => void;
}) {
  const desc = fieldDescriptor(fields, c.field);
  const meta = resolveRuleFieldMeta(desc?.meta);
  // A field with no descriptor (its plugin is gone, or the registry has
  // not answered yet) keeps the generic comparison editor its saved
  // operator implies, so the condition stays visible and removable.
  const numeric = meta?.valueClass ? meta.valueClass === "number" : c.operator === "range";
  const valueless = meta?.valueClass === "none";
  const operators = (meta?.operators ?? ["equals", "contains", "regex"]).filter(
    (op) => op !== "range",
  );
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
      <div className="min-w-0 sm:min-w-40 sm:flex-1">
        <SearchSelect
          {...searchSelectLabels()}
          ariaLabel={t("What it describes")}
          value={c.field}
          options={fieldOptions(fields, c.field)}
          onChange={(v) => onChange(retarget(c, v, fields))}
        />
      </div>
      {desc && isPluginDescriptor(desc) ? <PluginBadge owner={descriptorOwnerName(desc)} /> : null}
      {valueless ? (
        <p className="text-xs text-fg-muted sm:flex-1">{textOf(desc?.hint)}</p>
      ) : numeric ? (
        <div className="flex items-center gap-2">
          <Input
            className="w-24"
            type="number"
            min="0"
            step="any"
            aria-label={t("Minimum")}
            value={c.min ?? 0}
            onChange={(e) =>
              onChange({ ...c, min: e.target.value === "" ? 0 : Number(e.target.value) })
            }
          />
          <span className="text-xs text-fg-muted">{t("to")}</span>
          <Input
            className="w-24"
            type="number"
            min="0"
            step="any"
            aria-label={t("Maximum (0 means unlimited)")}
            value={c.max ?? 0}
            onChange={(e) =>
              onChange({ ...c, max: e.target.value === "" ? 0 : Number(e.target.value) })
            }
          />
          {meta?.unit ? <span className="text-xs text-fg-muted">{meta.unit}</span> : null}
        </div>
      ) : (
        <>
          {operators.length > 1 && (
            <Select
              className="w-full sm:w-32"
              aria-label={t("Comparison")}
              value={c.operator}
              onChange={(e) =>
                onChange({ ...c, operator: e.target.value as ReleaseCondition["operator"] })
              }
            >
              {operators.map((op) => (
                <option key={op} value={op}>
                  {operatorLabel(op)}
                </option>
              ))}
            </Select>
          )}
          <div className="min-w-0 sm:min-w-40 sm:flex-1">
            <ValueField
              value={c.value ?? ""}
              mono={c.operator === "regex"}
              placeholder={c.operator === "in" ? t("Values separated by commas") : undefined}
              suggestions={c.operator === "equals" ? suggestionsFor(desc) : []}
              onChange={(value) => onChange({ ...c, value })}
            />
          </div>
        </>
      )}
    </div>
  );
}

function ConditionGroup({
  values,
  fields,
  onChange,
}: {
  values: ReleaseCondition[];
  fields: RuleFieldDescriptor[];
  onChange: (v: ReleaseCondition[]) => void;
}) {
  return (
    <div className="space-y-2">
      {values.map((c, i) => (
        <div key={i} className="flex items-start gap-1">
          <div className="min-w-0 flex-1">
            <ConditionEditor
              value={c}
              fields={fields}
              onChange={(v) => onChange(values.map((x, j) => (i === j ? v : x)))}
            />
          </div>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("Remove condition")}
            onClick={() => onChange(values.filter((_, j) => i !== j))}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      ))}
      <Button
        size="sm"
        variant="ghost"
        disabled={values.length >= 16}
        onClick={() => onChange([...values, newCondition(fields)])}
      >
        <Plus size={14} />
        {t("Add condition")}
      </Button>
    </div>
  );
}

export function RuleEditorCard({
  rule: r,
  fields,
  handle,
  stepNumber,
  canMoveUp,
  canMoveDown,
  onMove,
  onChange,
  onRemove,
}: {
  rule: ReleaseRule;
  fields: RuleFieldDescriptor[];
  handle?: DragHandleProps;
  stepNumber?: number;
  canMoveUp?: boolean;
  canMoveDown?: boolean;
  onMove?: (dir: -1 | 1) => void;
  onChange: (patch: Partial<ReleaseRule>) => void;
  onRemove: () => void;
}) {
  const [advanced, setAdvanced] = useState(false);
  return (
    <article className={`space-y-3 px-3 py-3 ${r.disabled ? "opacity-60" : ""}`}>
      <div className="flex items-center gap-2">
        {handle ? <DragHandle handle={handle} label={t("Move rule")} /> : null}
        {stepNumber != null ? (
          <span className="w-5 text-xs tabular-nums text-fg-muted">{stepNumber}</span>
        ) : null}
        <Input
          aria-label={t("Rule name")}
          value={r.name}
          className="min-w-0 flex-1 font-medium"
          onChange={(e) => onChange({ name: e.target.value })}
        />
        <Switch
          aria-label={t("Enabled")}
          checked={!r.disabled}
          onChange={(e) => onChange({ disabled: !e.currentTarget.checked })}
        />
        <Button variant="ghost" size="icon" aria-label={t("Remove rule")} onClick={onRemove}>
          <Trash2 size={14} />
        </Button>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <Select
          aria-label={t("Action")}
          className="w-full font-medium sm:w-36"
          value={r.action}
          onChange={(e) =>
            onChange({
              action: e.target.value as ReleaseRule["action"],
              fallback: e.target.value === "require" ? r.fallback : [],
            })
          }
        >
          <option value="require">{t("Only play")}</option>
          <option value="prefer">{t("Prefer")}</option>
          <option value="avoid">{t("Never play")}</option>
        </Select>
        <span className="text-xs text-fg-muted">
          {r.action === "require"
            ? t("Must match to play.")
            : r.action === "avoid"
              ? t("Matching releases are skipped.")
              : t("Matching releases score higher.")}
        </span>
      </div>
      {r.conditions[0] && (
        <ConditionEditor
          value={r.conditions[0]}
          fields={fields}
          onChange={(v) => onChange({ conditions: [v, ...r.conditions.slice(1)] })}
        />
      )}
      <div className="flex items-center gap-1">
        <Button
          size="sm"
          variant="ghost"
          aria-expanded={advanced}
          onClick={() => setAdvanced((v) => !v)}
        >
          {t("Advanced")}
          <ChevronDown size={13} />
        </Button>
        <span className="flex-1" />
        {onMove ? (
          <>
            <Button
              size="icon"
              variant="ghost"
              disabled={!canMoveUp}
              aria-label={t("Move up")}
              onClick={() => onMove(-1)}
            >
              <ArrowUp size={13} />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              disabled={!canMoveDown}
              aria-label={t("Move down")}
              onClick={() => onMove(1)}
            >
              <ArrowDown size={13} />
            </Button>
          </>
        ) : null}
      </div>
      {advanced && (
        <div className="space-y-4 border-t border-border pt-3">
          <Select
            aria-label={t("Match conditions")}
            value={r.match ?? "all"}
            onChange={(e) => onChange({ match: e.target.value as "all" | "any" })}
          >
            <option value="all">{t("Match all conditions")}</option>
            <option value="any">{t("Match any condition")}</option>
          </Select>
          <div className="space-y-2">
            <p className="text-sm font-medium">{t("Other conditions")}</p>
            <ConditionGroup
              values={r.conditions.slice(1)}
              fields={fields}
              onChange={(rest) =>
                onChange({ conditions: [r.conditions[0]!, ...rest].filter(Boolean) })
              }
            />
          </div>
          <div className="space-y-2">
            <p className="text-sm font-medium">{t("Except when any matches")}</p>
            <ConditionGroup
              values={r.unless ?? []}
              fields={fields}
              onChange={(unless) => onChange({ unless })}
            />
          </div>
          {r.action === "require" && (
            <div className="space-y-2">
              <p className="text-sm font-medium">{t("Fallbacks")}</p>
              <p className="text-xs text-fg-muted">
                {t(
                  "Empty means no fallback. Matching alternatives are tried after primary matches.",
                )}
              </p>
              <ConditionGroup
                values={r.fallback ?? []}
                fields={fields}
                onChange={(fallback) => onChange({ fallback })}
              />
            </div>
          )}
          <p className="text-xs text-fg-muted">
            {t("Expressions ignore case. Lookarounds and backreferences are not supported.")}
          </p>
        </div>
      )}
    </article>
  );
}

type PreviewBody = { releases?: ConsoleRelease[]; warning?: string; note?: string };

/**
 * Filter rules (Only play / Never play) plus the rule builder and import/export
 * controls inside Section 1 ("Never play"). Adding a Prefer rule appends it to
 * releaseRules and scores it with the profile's custom formats.
 */
export function FilterRulesEditor({
  profile,
  onChange,
  error,
}: {
  profile: QualityProfile;
  onChange: (patch: Pick<QualityProfile, "releaseRules" | "customFormatScores">) => void;
  error?: string;
}) {
  useLocale();
  const { fields } = useRankingRegistry();
  const rules = profile.releaseRules ?? [];
  const filterRules = rules.filter((r) => r.action === "require" || r.action === "avoid");
  const [term, setTerm] = useState("");
  const [field, setField] = useState("name");
  const [action, setAction] = useState<ReleaseRule["action"]>("prefer");
  const [importError, setImportError] = useState("");
  const file = useRef<HTMLInputElement>(null);
  const profileRef = useRef(profile);
  const importAbort = useRef<AbortController | null>(null);
  profileRef.current = profile;
  useEffect(() => () => importAbort.current?.abort(), []);

  const conflict = releaseRuleConflict(rules, fields);

  /** The rules, with every Prefer rule scored as a custom format. */
  function commitRules(nextRules: ReleaseRule[], scores = profile.customFormatScores) {
    onChange({ releaseRules: nextRules, customFormatScores: withRuleScores(nextRules, scores) });
  }

  function updateRule(id: string, patch: Partial<ReleaseRule>) {
    commitRules(rules.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  }

  function removeRule(id: string) {
    commitRules(rules.filter((r) => r.id !== id));
  }

  function addTerm() {
    const value = term.trim();
    if (!value || rules.length >= 32) return;
    const c = newCondition(fields, field);
    if (resolveRuleFieldMeta(fieldDescriptor(fields, field)?.meta)?.valueClass === "none") {
      c.value = "";
    } else if (c.operator === "range") {
      const [min, max] = value.split(/[-–]/).map((part) => part.trim());
      c.min = min ? Number(min) : 0;
      c.max = max != null && max !== "" ? Number(max) : c.min;
    } else {
      c.value = value;
    }
    commitRules([...rules, createRule(value, c, action)]);
    setTerm("");
  }

  async function importRules(f: File) {
    const startRules = JSON.stringify(profileRef.current.releaseRules ?? []);
    const controller = new AbortController();
    importAbort.current?.abort();
    importAbort.current = controller;
    try {
      if (f.size > 32768) throw Error(t("Rules file is too large."));
      const text = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result ?? ""));
        reader.onerror = () => reject(Error(t("Could not read the rules file.")));
        reader.readAsText(f);
      });
      const data = JSON.parse(text) as ReleaseRule[];
      const currentRules = profileRef.current.releaseRules ?? [];
      if (!Array.isArray(data) || data.length + currentRules.length > 32)
        throw Error(t("Use a rules export with at most 32 rules."));
      const next = data.map((r) => ({ ...r, id: uuid() }));
      const mergedRules = [...currentRules, ...next];
      const scores = withRuleScores(mergedRules, profileRef.current.customFormatScores);
      const body = await apiFetch<PreviewBody>("/releases/rules/preview", {
        method: "POST",
        signal: controller.signal,
        body: JSON.stringify({
          profile: { ...profileRef.current, releaseRules: mergedRules, customFormatScores: scores },
          names: [],
        }),
      });
      if (controller.signal.aborted) return;
      if (JSON.stringify(profileRef.current.releaseRules ?? []) !== startRules) {
        setImportError(t("The profile changed during import. Nothing was added."));
        return;
      }
      if (body.warning) throw Error(body.warning);
      onChange({ releaseRules: mergedRules, customFormatScores: scores });
      setImportError("");
    } catch (e) {
      if (isAbort(e) || controller.signal.aborted) return;
      setImportError(errorMessage(e));
    }
  }
  function exportRules() {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(rules, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = "release-rules.json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return (
    <div className="space-y-4 border-t border-border px-5 py-4">
      <div className="space-y-1">
        <h4 className="text-sm font-medium text-fg">{t("Rules")}</h4>
        <p className="text-xs text-fg-muted">
          {t(
            "Only play and Never play rules filter releases here. Prefer rules are scored with your custom formats below.",
          )}
        </p>
      </div>
      <div className="space-y-2">
        <Input
          aria-label={t("Add rule")}
          placeholder={t("Type a language, tag or release group…")}
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && term.trim() && rules.length < 32) {
              e.preventDefault();
              addTerm();
            }
          }}
        />
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <div className="min-w-0 sm:flex-1">
            <SearchSelect
              {...searchSelectLabels()}
              ariaLabel={t("What it describes")}
              value={field}
              options={fieldOptions(fields, field)}
              onChange={setField}
            />
          </div>
          <Select
            aria-label={t("Action")}
            className="w-full sm:w-36"
            value={action}
            onChange={(e) => setAction(e.target.value as ReleaseRule["action"])}
          >
            <option value="require">{t("Only play")}</option>
            <option value="prefer">{t("Prefer")}</option>
            <option value="avoid">{t("Never play")}</option>
          </Select>
          <Button size="sm" disabled={rules.length >= 32 || !term.trim()} onClick={addTerm}>
            <Plus size={14} />
            {t("Add rule")}
          </Button>
        </div>
        {resolveRuleFieldMeta(fieldDescriptor(fields, field)?.meta)?.valueClass === "none" && (
          <p className="text-xs text-fg-muted">{textOf(fieldDescriptor(fields, field)?.hint)}</p>
        )}
      </div>
      {(error || importError || conflict) && (
        <p role="alert" className="text-sm text-danger">
          {error || importError || conflict}
        </p>
      )}
      {filterRules.length > 0 && (
        <div className="divide-y divide-border overflow-hidden rounded-lg border border-border">
          {filterRules.map((r) => (
            <RuleEditorCard
              key={r.id}
              rule={r}
              fields={fields}
              onChange={(patch) => updateRule(r.id, patch)}
              onRemove={() => removeRule(r.id)}
            />
          ))}
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="ghost" size="sm" onClick={() => file.current?.click()}>
          <Upload size={14} />
          {t("Import rules")}
        </Button>
        <Button variant="ghost" size="sm" disabled={!rules.length} onClick={exportRules}>
          <Download size={14} />
          {t("Export rules")}
        </Button>
        <input
          ref={file}
          type="file"
          accept="application/json,.json"
          className="hidden"
          aria-label={t("Import rules")}
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void importRules(f);
            e.target.value = "";
          }}
        />
      </div>
    </div>
  );
}

function originalPreviewNote(mode: "names" | "title", rules: ReleaseRule[], serverNote: string) {
  if (!rules.some(mentionsOriginal)) return "";
  if (mode === "names" || serverNote === "original_language_unavailable") {
    return t(
      "Original-language audio cannot be compared from a filename. Search a title to use the catalog language.",
    );
  }
  if (serverNote === "original_language_unknown") {
    return t("This title has no original language, so original-language audio stays unverified.");
  }
  return "";
}

function explanationLabel(outcome: string, state: string, original: boolean) {
  if (outcome === "unknown" && original) return t("Original-language audio was not confirmed.");
  if (outcome === "unknown") return t("Needs verification");
  if (outcome === "exclude") return t("Excluded");
  if (outcome === "fallback") return t("Fallback");
  if (outcome === "prefer") return t("Preferred");
  if (outcome === "skip") return t("Exception matched");
  if (state === "match") return t("Matches");
  return t("Does not match");
}

export function RulePreview({ profile }: { profile: QualityProfile }) {
  const [mode, setMode] = useState<"names" | "title">("names");
  const [names, setNames] = useState("");
  const [key, setKey] = useState("");
  const [title, setTitle] = useState("");
  const [result, setResult] = useState<ConsoleRelease[] | null>(null);
  const [warning, setWarning] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [requested, setRequested] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const rules = profile.releaseRules ?? [];
  const serialized = JSON.stringify(profile);
  const originalNote = originalPreviewNote(mode, rules, note);
  useEffect(() => {
    if (!requested) return;
    const empty = mode === "title" ? !key : !names.trim();
    if (empty) {
      setResult(null);
      setWarning("");
      setNote("");
      setError("");
      setBusy(false);
      return;
    }
    const abort = new AbortController();
    let current = true;
    setError("");
    setBusy(true);
    const timer = setTimeout(() => {
      void apiFetch<PreviewBody>("/releases/rules/preview", {
        method: "POST",
        signal: abort.signal,
        body: JSON.stringify({
          profile: JSON.parse(serialized) as QualityProfile,
          ...(mode === "title" ? { key } : { names: names.split("\n").filter((x) => x.trim()) }),
        }),
      })
        .then((body) => {
          if (!current) return;
          setResult(body.releases ?? []);
          setWarning(body.warning ?? "");
          setNote(body.note ?? "");
        })
        .catch((e) => {
          if (!current || isAbort(e)) return;
          setError(errorMessage(e));
          setResult(null);
        })
        .finally(() => {
          if (current) setBusy(false);
        });
    }, 350);
    return () => {
      current = false;
      clearTimeout(timer);
      abort.abort();
    };
  }, [serialized, names, key, mode, requested, attempt]);
  return (
    <div className="space-y-3 border-t border-border px-5 py-4">
      <h4 className="text-sm font-medium">{t("Try before saving")}</h4>
      <Select
        aria-label={t("Preview source")}
        value={mode}
        onChange={(e) => {
          setRequested(false);
          setResult(null);
          setWarning("");
          setNote("");
          setError("");
          setBusy(false);
          setMode(e.target.value as "names" | "title");
        }}
      >
        <option value="names">{t("Paste release names")}</option>
        <option value="title">{t("Search a title")}</option>
      </Select>
      {mode === "names" ? (
        <Textarea
          aria-label={t("Release names")}
          rows={3}
          value={names}
          onChange={(e) => setNames(e.target.value)}
        />
      ) : (
        <>
          <TitlePicker
            onPick={(k, n) => {
              setKey(k);
              setTitle(n);
            }}
          />
          {title && <p className="text-xs text-fg-muted">{title}</p>}
        </>
      )}
      <p className="text-xs text-fg-muted">
        {mode === "names"
          ? t("Filename preview checks rules only. Search a title to include full profile ranking.")
          : t(
              "Uses your indexers. No settings are saved and no media is downloaded. Device limits apply when playing.",
            )}
      </p>
      <Button
        size="sm"
        variant="outline"
        disabled={busy || (mode === "title" ? !key : !names.trim())}
        onClick={() => {
          setRequested(true);
          setAttempt((n) => n + 1);
        }}
      >
        {busy ? t("Checking…") : requested ? t("Check again") : t("Preview")}
      </Button>
      <div aria-live="polite" className="space-y-2">
        {originalNote && <p className="text-sm text-fg-muted">{originalNote}</p>}
        {warning && (
          <p role="alert" className="text-sm text-danger">
            {warning}
          </p>
        )}
        {error && (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        )}
        {result?.length === 0 && <p className="text-sm text-fg-muted">{t("No releases found")}</p>}
        {result?.map((row, i) => (
          <div key={`${row.release.id}-${i}`} className="rounded-lg border border-border p-3">
            <div className="mb-2 flex items-start gap-2">
              <span className="min-w-0 flex-1 break-all text-xs font-medium">
                {row.release.name}
              </span>
              <Badge
                variant={
                  row.rejected ? "warning" : row.preferences?.fallback ? "secondary" : "success"
                }
              >
                {row.rejected
                  ? row.preferences?.unknown
                    ? t("Needs verification")
                    : t("Excluded")
                  : row.preferences?.fallback
                    ? t("Fallback")
                    : t("Matches")}
              </Badge>
            </div>
            {row.reason && <p className="mb-1 text-xs text-danger">{row.reason}</p>}
            {row.preferences?.explanations.map((e) => (
              <p key={e.id} className="text-xs text-fg-muted">
                <span className="font-medium">{e.name}</span>
                {" · "}
                {explanationLabel(
                  e.outcome,
                  e.state,
                  rules.some((rule) => rule.id === e.id && mentionsOriginal(rule)),
                )}
              </p>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
