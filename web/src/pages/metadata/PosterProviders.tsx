import { PosterFolder } from "./PosterFolder";
import { Button, SearchSelect, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { MutationError } from "../../components/feedback";
import { t } from "../../i18n";
import { api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { normalizePosters, posterPayload, POSTER_SERVICES } from "../settings/PosterAccounts";
export function PosterProviders({ onSaved }: { onSaved: () => void }) {
  const config = useAsync(async () => normalizePosters((await api.getConfig()).posters), []);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  return (
    <section className="metadata-control-section">
      <div className="metadata-section-title">
        <h3 data-setting="poster-source">{t("Poster source")}</h3>
      </div>
      <SearchSelect
        ariaLabel={t("Poster source")}
        options={[
          { value: "tmdb", label: "TMDB" },
          ...POSTER_SERVICES.filter((s) => config.data?.accounts?.[s.id]?.enabled).map((s) => ({
            value: s.id,
            label: s.name,
          })),
        ]}
        disabled={!config.data || saving}
        value={config.data?.source ?? "tmdb"}
        onChange={async (source) => {
          setSaving(true);
          setError(null);
          try {
            const result = await apiFetch<{ value: NonNullable<typeof config.data> }>(
              "/config/posters",
              {
                method: "PUT",
                body: JSON.stringify(posterPayload({ ...config.data!, source })),
              },
            );
            config.setData(result.value);
            onSaved();
            toast.success(t("Poster provider saved"));
          } catch (e) {
            setError(e);
          } finally {
            setSaving(false);
          }
        }}
      />
      {config.data && (
        <PosterFolder
          value={config.data.assetFolder ?? ""}
          save={async (assetFolder) => {
            const result = await apiFetch<{ value: NonNullable<typeof config.data> }>(
              "/config/posters",
              {
                method: "PUT",
                body: JSON.stringify(posterPayload({ ...config.data!, assetFolder })),
              },
            );
            config.setData(result.value);
            onSaved();
          }}
        />
      )}
      {error || config.error ? (
        <>
          <MutationError error={error || config.error} />
          <Button onClick={() => void config.reload()}>{t("Retry")}</Button>
        </>
      ) : null}
    </section>
  );
}
