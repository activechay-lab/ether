import type { BugSettings } from "./model";
export const defaultBug: Required<BugSettings> = {
  position: "bottomRight",
  opacity: 0.3,
  style: "monochrome",
  size: "medium",
  source: "logo",
  fadeBreaks: true,
  fadeCredits: false,
};
export function resolveBug(base?: BugSettings, override?: BugSettings) {
  return { ...defaultBug, ...base, ...override };
}
