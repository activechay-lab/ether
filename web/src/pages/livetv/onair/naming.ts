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
    }[option ?? "styles"] ?? t(STYLE_NAME)
  );
}
