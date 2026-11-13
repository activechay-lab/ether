import { localized, t } from "../../../i18n";
import type { FieldGroup } from "../../settings/SchemaForm";

export const UpcomingEpisodeSettings: FieldGroup = localized(() => ({
  title: t("Upcoming content"),
  fields: [
    {
      key: "showUpcomingMovies",
      label: t("Show upcoming movies"),
      kind: "bool",
      help: t("List movies before their release date."),
    },
    {
      key: "showUpcomingEpisodes",
      label: t("Show upcoming episodes"),
      kind: "bool",
      help: t("List upcoming episodes in seasons that are already airing."),
    },
  ],
}));
