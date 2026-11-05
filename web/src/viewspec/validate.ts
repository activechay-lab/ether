/**
 * Client-side validation for plugin ViewSpec documents. The server validates
 * with internal/viewspec first; the console validates again before render so
 * a document is never trusted just because it arrived over the owner API.
 *
 * Structural validation is the AppKit package's own `validateSpec` (closed
 * unions, depth walk before parse, prototype path guards). Ether widget and
 * frame names (`ether.posterGrid`, …) are not slugs the upstream schema
 * accepts, so they are normalized to slugs on a copy for the structural pass
 * while the Ether rules below check the real names: closed registry, prop
 * shapes, no class-name fields anywhere, search-only filter bars, and the
 * app table variant.
 */
import { validateSpec, type PageSpec } from "@braedonsaunders/appkit-viewspec";
import {
  CLASS_FIELDS,
  FRAME_SHAPES,
  WIDGET_SHAPES,
  checkProps,
  isEtherFrame,
  isEtherWidget,
} from "./registry";

type PluginSpecValidation =
  { ok: true; spec: PageSpec; errors: [] } | { ok: false; errors: string[] };

function isObj(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Deep-copy with ether.* widget/frame names rewritten to upstream slugs. */
function normalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalize);
  if (!isObj(value)) return value;
  const out: Record<string, unknown> = {};
  for (const [key, child] of Object.entries(value)) {
    if ((key === "widget" || key === "frame") && typeof child === "string") {
      out[key] = child
        .replace(/^ether\./, "ether-")
        .replace(/([a-z])([A-Z])/g, "$1-$2")
        .toLowerCase();
    } else {
      out[key] = normalize(child);
    }
  }
  return out;
}

/** The Ether plugin rules, walked over the raw document. */
function etherRules(value: unknown, path: string, errors: string[]): void {
  if (Array.isArray(value)) {
    value.forEach((item, i) => etherRules(item, `${path}.${i}`, errors));
    return;
  }
  if (!isObj(value)) return;
  for (const key of Object.keys(value)) {
    if (CLASS_FIELDS.has(key)) {
      errors.push(`${path}.${key}: class-name fields are not allowed in plugin documents`);
    }
  }
  const kind = value.kind;
  if (kind === "widget" || (typeof value.widget === "string" && !("kind" in value))) {
    const name = value.widget;
    if (typeof name === "string") {
      if (!isEtherWidget(name)) {
        errors.push(`${path}.widget: unknown Ether widget "${name}"`);
      } else {
        const props = isObj(value.props) ? value.props : {};
        for (const e of checkProps(WIDGET_SHAPES[name], props, { allowRefs: true })) {
          errors.push(`${path}.props: ${e}`);
        }
      }
    }
  }
  if (kind === "frame" && typeof value.frame === "string") {
    if (!isEtherFrame(value.frame)) {
      errors.push(`${path}.frame: unknown Ether frame "${value.frame}"`);
    } else {
      const props = isObj(value.props) ? value.props : {};
      for (const e of checkProps(FRAME_SHAPES[value.frame], props, { allowRefs: true })) {
        errors.push(`${path}.props: ${e}`);
      }
    }
  }
  if (kind === "filter-bar") {
    const controls = isObj(value.controls) ? value.controls : {};
    for (const [name, on] of Object.entries(controls)) {
      if (name !== "search" && on === true) {
        errors.push(
          `${path}.controls.${name}: filter-bar control "${name}" is not available in plugin documents`,
        );
      }
    }
    for (const key of [
      "dimensions",
      "subsidiaries",
      "customers",
      "dateRange",
      "primaryFilter",
      "periodPresets",
      "extraPeriods",
      "extraPeriodsLabel",
      "defaultPeriod",
    ]) {
      if (key in value) {
        errors.push(`${path}.${key}: ${key} is not available in plugin documents`);
      }
    }
  }
  if (kind === "table" && "variant" in value && value.variant !== "app") {
    errors.push(`${path}.variant: plugin tables use the app variant`);
  }
  for (const [key, child] of Object.entries(value)) {
    etherRules(child, path ? `${path}.${key}` : key, errors);
  }
}

/**
 * Validate an untrusted plugin document. Returns the parsed PageSpec only
 * when every rule passes; callers must render from the returned spec, never
 * from the raw value they started with.
 */
export function validatePluginSpec(raw: unknown): PluginSpecValidation {
  const errors: string[] = [];
  etherRules(raw, "", errors);
  const structural = validateSpec(normalize(raw));
  if (!structural.ok) errors.push(...structural.errors);
  if (errors.length > 0 || !structural.ok) return { ok: false, errors };
  // The structural pass ran on a name-normalized copy; hand back the real
  // document, now known to have the same shape, so widget names survive.
  return { ok: true, spec: raw as PageSpec, errors: [] };
}
