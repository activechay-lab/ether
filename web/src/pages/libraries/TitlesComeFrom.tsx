import { SettingsSection } from "@braedonsaunders/appkit-ui";
import { Link } from "react-router-dom";
import { t } from "../../i18n";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
export function TitlesComeFrom({library}: {library: Library}) {
  const registry = useAsync(signal => librariesApi.catalogueSources(signal), []);
  return <SettingsSection title={t("Titles come from")}>
    <div className="flex flex-col gap-3">{registry.data?.filter(source => source.libraries.some(lib => lib.id === library.id)).map(source => <span key={source.id}>{source.name}</span>)}
      <Link className="text-primary underline" to="/libraries/sources">{t("Catalogue sources")}</Link>
    </div>
  </SettingsSection>;
}
