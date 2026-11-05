import { Badge, SettingsSection } from "@braedonsaunders/appkit-ui";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";

type ShareFilterPart = { kind: string; values: string[] };

type PlexViewerDefaults = {
  audioLanguage?: string;
  autoSelectAudio?: boolean;
  subtitleLanguage?: string;
  subtitleMode?: string;
  hasProfile?: boolean;
  filterMovies?: string;
  filterTelevision?: string;
  filterMoviesParts?: ShareFilterPart[];
  filterTelevisionParts?: ShareFilterPart[];
  allowSync?: boolean | null;
  hasSettings?: boolean;
  labelNote?: string;
};

function modeLabel(mode: string | undefined): string {
  switch (mode) {
    case "foreign-only":
      return t("Only for foreign audio");
    case "always":
      return t("Always on");
    case "forced-only":
      return t("Forced tracks only");
    case "off":
      return t("Off unless I turn them on");
    default:
      return mode || t("None");
  }
}

function yesNo(v: boolean | null | undefined): string {
  if (v == null) return t("Not set");
  return v ? t("Yes") : t("No");
}

function formatPart(part: ShareFilterPart): string {
  const list = part.values.join(", ");
  const name = part.values[0] || "";
  switch (part.kind) {
    case "rating-allow":
      return t("Ratings: {list}", { list });
    case "rating-deny":
      return t("Hides ratings: {list}", { list });
    case "rating-max":
      return t("Up to {rating}", { rating: name });
    case "rating-min":
      return t("From {rating}", { rating: name });
    case "label-allow":
      return t("Shows collection {name}", { name: list });
    case "label-deny":
      return t("Hides collection {name}", { name: list });
    default:
      return list;
  }
}

function formatFilter(parts: ShareFilterPart[] | undefined, raw: string | undefined): string {
  if (parts && parts.length > 0) {
    return parts.map(formatPart).join(" · ");
  }
  return raw || t("None");
}

/**
 * Read-only plex.tv account and share values. Ether owner rules still win.
 */
export function FromPlexDefaults({
  viewerId,
  show,
}: {
  viewerId: number;
  show: "tracks" | "share";
}) {
  const config = useAsync(() => api.getConfig(), []);
  const data = useAsync(
    (signal) =>
      apiFetch<PlexViewerDefaults>(`/viewer-plex-defaults/${viewerId}`, { signal }).catch(
        () => null,
      ),
    [viewerId],
  );
  const d = data.data;
  if (!d || config.data?.frontends?.plex?.enabled === false || !config.data) return null;
  if (show === "tracks" && !d.hasProfile && !d.audioLanguage && !d.subtitleLanguage) return null;
  if (
    show === "share" &&
    !d.hasSettings &&
    !d.filterMovies &&
    !d.filterTelevision &&
    d.allowSync == null
  )
    return null;
  return (
    <SettingsSection
      title={t("From Plex")}
      description={t("Read-only defaults from this viewer's plex.tv account. Ether rules win.")}
    >
      {show === "tracks" ? (
        <>
          <SettingsRow
            title={t("Audio language")}
            control={<Badge>{d.audioLanguage || t("None")}</Badge>}
          />
          <SettingsRow
            title={t("Auto-select audio")}
            control={<Badge>{yesNo(d.autoSelectAudio)}</Badge>}
          />
          <SettingsRow
            title={t("Subtitle language")}
            control={<Badge>{d.subtitleLanguage || t("None")}</Badge>}
          />
          <SettingsRow
            title={t("Subtitle mode")}
            control={<Badge>{modeLabel(d.subtitleMode)}</Badge>}
          />
        </>
      ) : (
        <>
          <SettingsRow
            title={t("Movie filter")}
            control={<Badge>{formatFilter(d.filterMoviesParts, d.filterMovies)}</Badge>}
          />
          <SettingsRow
            title={t("TV filter")}
            control={<Badge>{formatFilter(d.filterTelevisionParts, d.filterTelevision)}</Badge>}
          />
          <SettingsRow title={t("Downloads")} control={<Badge>{yesNo(d.allowSync)}</Badge>} />
          <p className="px-5 py-2 text-xs text-fg-muted">
            {t(
              "Label filters apply to the TMDB collection name only. Titles without a collection name are not hidden.",
            )}
          </p>
        </>
      )}
    </SettingsSection>
  );
}
