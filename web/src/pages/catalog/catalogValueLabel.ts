import { t } from "../../i18n";
/** Translate finite enum labels at render time; wire values remain canonical. */
export function catalogValueLabel(field: string, value: string): string {
  if (field === "playable" || field === "cached") {
    if (value === "yes") return t("Yes");
    if (value === "unknown") return t("Unknown");
  }
  if (field === "quality") {
    if (value === "4k") return "4K";
    if (value === "hdr") return "HDR";
    if (value === "dv") return "Dolby Vision";
    if (value === "atmos") return "Dolby Atmos";
  }
  if (field === "type") {
    if (value === "movie") return t("Movie");
    if (value === "show") return t("TV show");
  }
  if (field === "watched") {
    if (value === "watched") return t("Watched");
    if (value === "unwatched") return t("Unwatched");
  }
  if (["progress", "rated", "watchlist", "anime", "kids"].includes(field)) {
    if (value === "true") return t("Yes");
    if (value === "false") return t("No");
  }
  if (field === "status") {
    if (value === "Returning Series") return t("Returning series");
    if (value === "Ended") return t("Ended");
    if (value === "Canceled") return t("Cancelled");
    if (value === "In Production") return t("In production");
  }
  return value;
}
