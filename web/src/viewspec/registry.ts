/**
 * The closed Ether widget and frame registry for plugin ViewSpec documents
 * (docs/extend/SPEC.md §5.6). A plugin may only name these; the Go validator
 * in internal/viewspec carries the same list and prop shapes. Each entry
 * validates its props twice: structurally against the spec (validate.ts,
 * where field references cannot be type-checked yet) and again here after
 * resolution, before anything renders.
 */
import { isFieldRef } from "@braedonsaunders/appkit-viewspec";

const ETHER_WIDGETS = [
  "ether.posterGrid",
  "ether.posterShelf",
  "ether.titleCard",
  "ether.liveChart",
  "ether.settingsForm",
  "ether.actionButton",
  "ether.titleMatch",
  "ether.healthStrip",
  "ether.channelRow",
  "ether.badge",
] as const;

const ETHER_FRAMES = ["ether.section", "ether.tabs", "ether.drawer"] as const;

type EtherWidget = (typeof ETHER_WIDGETS)[number];
type EtherFrame = (typeof ETHER_FRAMES)[number];

export function isEtherWidget(name: string): name is EtherWidget {
  return (ETHER_WIDGETS as readonly string[]).includes(name);
}

export function isEtherFrame(name: string): name is EtherFrame {
  return (ETHER_FRAMES as readonly string[]).includes(name);
}

/** Field names a plugin document may never carry: no plugin CSS, ever. */
export const CLASS_FIELDS = new Set([
  "className",
  "bodyClassName",
  "itemClassName",
  "actionsClassName",
  "headerClassName",
  "fallbackClassName",
  "labelClassName",
]);

type JsonKind = "string" | "number" | "bool" | "array" | "object";

interface PropShape {
  required?: string[];
  optional?: string[];
  /** Groups where at least one name must be present. */
  oneOf?: string[][];
  /** JSON kinds a literal (or resolved) value may have, per prop. */
  kinds?: Record<string, JsonKind[]>;
}

export const WIDGET_SHAPES: Record<EtherWidget, PropShape> = {
  "ether.posterGrid": {
    required: ["items"],
    optional: ["empty"],
    kinds: { items: ["array"], empty: ["string"] },
  },
  "ether.posterShelf": {
    required: ["items"],
    optional: ["label", "empty"],
    kinds: { items: ["array"], label: ["string"], empty: ["string"] },
  },
  "ether.titleCard": {
    oneOf: [["item", "ratingKey", "tmdbId"]],
    optional: ["item", "ratingKey", "tmdbId", "mediaType", "reason"],
    kinds: {
      item: ["object"],
      ratingKey: ["string", "number"],
      tmdbId: ["number"],
      mediaType: ["string"],
      reason: ["string"],
    },
  },
  "ether.liveChart": {
    required: ["series"],
    optional: ["times", "max", "unit", "label", "area"],
    kinds: {
      series: ["array"],
      times: ["array"],
      max: ["number"],
      unit: ["string"],
      label: ["string"],
      area: ["bool"],
    },
  },
  "ether.settingsForm": {
    required: ["groups", "values", "action"],
    optional: ["submitLabel"],
    kinds: {
      groups: ["array"],
      values: ["object"],
      action: ["string"],
      submitLabel: ["string"],
    },
  },
  "ether.actionButton": {
    required: ["label", "action"],
    optional: ["params", "variant"],
    kinds: {
      label: ["string"],
      action: ["string"],
      params: ["object"],
      variant: ["string"],
    },
  },
  "ether.titleMatch": {
    required: ["action"],
    optional: ["label", "placeholder", "params", "mediaType"],
    kinds: {
      action: ["string"],
      label: ["string"],
      placeholder: ["string"],
      params: ["object"],
      mediaType: ["string"],
    },
  },
  "ether.healthStrip": {
    required: ["items"],
    kinds: { items: ["array"] },
  },
  "ether.channelRow": {
    required: ["name"],
    optional: ["number", "group", "logo", "nowPlaying", "level"],
    kinds: {
      name: ["string"],
      number: ["string", "number"],
      group: ["string"],
      logo: ["string"],
      nowPlaying: ["string"],
      level: ["string"],
    },
  },
  "ether.badge": {
    required: ["label"],
    optional: ["variant"],
    kinds: { label: ["string"], variant: ["string"] },
  },
};

export const FRAME_SHAPES: Record<EtherFrame, PropShape> = {
  "ether.section": {
    optional: ["title", "description"],
    kinds: { title: ["string"], description: ["string"] },
  },
  "ether.tabs": {
    required: ["tabs"],
    optional: ["param"],
    kinds: { tabs: ["array"], param: ["string"] },
  },
  "ether.drawer": {
    required: ["title"],
    optional: ["openLabel"],
    kinds: { title: ["string"], openLabel: ["string"] },
  },
};

function jsonKind(value: unknown): JsonKind {
  if (Array.isArray(value)) return "array";
  if (value !== null && typeof value === "object") return "object";
  if (typeof value === "boolean") return "bool";
  if (typeof value === "number") return "number";
  return "string";
}

/**
 * Check props against a shape. With `allowRefs`, a field reference counts as
 * present and skips the kind check (spec-level validation); without it, the
 * values are resolved data and must match (render-time validation).
 */
export function checkProps(
  shape: PropShape,
  props: Record<string, unknown>,
  opts: { allowRefs: boolean },
): string[] {
  const errors: string[] = [];
  const allowed = new Set([...(shape.required ?? []), ...(shape.optional ?? [])]);
  for (const [name, value] of Object.entries(props)) {
    if (!allowed.has(name)) {
      errors.push(`unknown prop "${name}"`);
      continue;
    }
    if (opts.allowRefs && isFieldRef(value)) continue;
    const kinds = shape.kinds?.[name];
    if (kinds && !kinds.includes(jsonKind(value))) {
      errors.push(`prop "${name}" must be ${kinds.join(" or ")}`);
    }
  }
  for (const name of shape.required ?? []) {
    if (!(name in props)) errors.push(`prop "${name}" is required`);
  }
  for (const group of shape.oneOf ?? []) {
    if (!group.some((name) => name in props)) {
      errors.push(`one of ${group.join(", ")} is required`);
    }
  }
  return errors;
}

/** Render-time prop check for a widget, after field references resolve. */
export function validateWidgetProps(name: string, props: Record<string, unknown>): string[] {
  if (!isEtherWidget(name)) return [`unknown Ether widget "${name}"`];
  return checkProps(WIDGET_SHAPES[name], props, { allowRefs: false });
}

/** Render-time prop check for a frame, after field references resolve. */
export function validateFrameProps(name: string, props: Record<string, unknown>): string[] {
  if (!isEtherFrame(name)) return [`unknown Ether frame "${name}"`];
  return checkProps(FRAME_SHAPES[name], props, { allowRefs: false });
}
