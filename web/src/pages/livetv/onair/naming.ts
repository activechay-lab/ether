import { t } from "../../../i18n";
// Owner-facing terminology lives here. Stable record and API identifiers remain `look`.
export const STYLE_NAME = "Styles";
export function stylesName() {
  const option = new URLSearchParams(window.location.search).get("namePreview");
  return (
    {
      styles: t("Styles"),
      themes: t("Themes"),
      branding: t("Branding"),
      package: t("On-air package"),
    }[option ?? ""] ?? t(STYLE_NAME)
  );
}

// All feature terminology is editable here; keys remain stable for callers.
const copy = {
  "A late-night style for {name}": (values?: Parameters<typeof t>[1]) =>
    t("A late-night style for {name}", values),
  "Apply style": (values?: Parameters<typeof t>[1]) => t("Apply style", values),
  "Channels using this style will lose their on-air graphics.": (
    values?: Parameters<typeof t>[1],
  ) => t("Channels using this style will lose their on-air graphics.", values),
  "Choose a style": (values?: Parameters<typeof t>[1]) => t("Choose a style", values),
  "Couldn\u2019t save style": (values?: Parameters<typeof t>[1]) =>
    t("Couldn\u2019t save style", values),
  "Default ({style})": (values?: Parameters<typeof t>[1]) => t("Default ({style})", values),
  "Default style": (values?: Parameters<typeof t>[1]) => t("Default style", values),
  "Default style \u00b7 {name}": (values?: Parameters<typeof t>[1]) =>
    t("Default style \u00b7 {name}", values),
  "Describe your style": (values?: Parameters<typeof t>[1]) => t("Describe your style", values),
  "Duplicate a style": (values?: Parameters<typeof t>[1]) => t("Duplicate a style", values),
  "Edit style": (values?: Parameters<typeof t>[1]) => t("Edit style", values),
  "Fallback screens without a style": (values?: Parameters<typeof t>[1]) =>
    t("Fallback screens without a style", values),
  "Give your channels a style": (values?: Parameters<typeof t>[1]) =>
    t("Give your channels a style", values),
  "Match the style to this channel\u2019s programming": (values?: Parameters<typeof t>[1]) =>
    t("Match the style to this channel\u2019s programming", values),
  "Matches mood keywords to a complete built-in style. No AI required.": (
    values?: Parameters<typeof t>[1],
  ) => t("Matches mood keywords to a complete built-in style. No AI required.", values),
  "New style": (values?: Parameters<typeof t>[1]) => t("New style", values),
  "New style\u2026": (values?: Parameters<typeof t>[1]) => t("New style\u2026", values),
  "No style": (values?: Parameters<typeof t>[1]) => t("No style", values),
  "Save style": (values?: Parameters<typeof t>[1]) => t("Save style", values),
  "Search styles": (values?: Parameters<typeof t>[1]) => t("Search styles", values),
  "Search styles\u2026": (values?: Parameters<typeof t>[1]) => t("Search styles\u2026", values),
  Style: (values?: Parameters<typeof t>[1]) => t("Style", values),
  "Style actions": (values?: Parameters<typeof t>[1]) => t("Style actions", values),
  "Style applied": (values?: Parameters<typeof t>[1]) => t("Style applied", values),
  "Style inspector": (values?: Parameters<typeof t>[1]) => t("Style inspector", values),
  "Style name": (values?: Parameters<typeof t>[1]) => t("Style name", values),
  "Style \u00b7 {name}": (values?: Parameters<typeof t>[1]) => t("Style \u00b7 {name}", values),
  "Style's break screen": (values?: Parameters<typeof t>[1]) => t("Style's break screen", values),
  "Untitled style": (values?: Parameters<typeof t>[1]) => t("Untitled style", values),
  "Use default style": (values?: Parameters<typeof t>[1]) => t("Use default style", values),
  "Your changes to this style haven\u2019t been saved.": (values?: Parameters<typeof t>[1]) =>
    t("Your changes to this style haven\u2019t been saved.", values),
};
export function styleCopy(message: keyof typeof copy, values?: Parameters<typeof t>[1]) {
  return copy[message](values);
}
