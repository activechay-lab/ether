import { Button, SearchSelect } from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { Field } from "../../components/fields";
import { TitleSearch } from "../../components/pickers";
import { t } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { LANGUAGE_TAGS } from "../libraries/browser/CatalogPickers";
import { apiFetch, errorMessage } from "../../lib/api";
import type { TitleCard } from "../../lib/contract";

type SearchResult = {
  session: string;
  title: string;
  offers: { id: string; name: string; language: string; provider: string }[];
};
type CheckResult = { bytes: number; format: string; cues: number; playbackVerified: boolean };

/** The same provider check for every plugin using the subtitle contract. */
export function SubtitleCheck({
  provider,
  onClose,
}: {
  provider: { id: string; name: string };
  onClose: () => void;
}) {
  const { locale } = useLocale();
  const languages = [...new Set(LANGUAGE_TAGS.map((tag) => tag.split("-")[0]!))]
    .map((value) => ({
      value,
      label: new Intl.DisplayNames([locale], { type: "language" }).of(value) || value,
    }))
    .sort((a, b) => a.label.localeCompare(b.label, locale));
  const [title, setTitle] = useState<TitleCard | null>(null);
  const [language, setLanguage] = useState("en");
  const [search, setSearch] = useState<SearchResult | null>(null);
  const [result, setResult] = useState<CheckResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const session = useRef<string | null>(null);
  const generation = useRef(0);
  const cleanup = async () => {
    generation.current++;
    const id = session.current;
    session.current = null;
    if (id)
      await apiFetch(`/plugin-tests/subtitles/${encodeURIComponent(id)}`, {
        method: "DELETE",
      }).catch(() => {});
  };
  useEffect(
    () => () => {
      void cleanup();
    },
    [],
  );
  const find = async () => {
    if (!title) return;
    setBusy(true);
    setError("");
    setResult(null);
    setSearch(null);
    try {
      await cleanup();
      const ticket = generation.current;
      const response = await apiFetch<SearchResult>("/plugin-tests/subtitles/search", {
        method: "POST",
        body: JSON.stringify({ plugin: provider.id, key: title.key, language }),
      });
      if (ticket !== generation.current) {
        await apiFetch(`/plugin-tests/subtitles/${encodeURIComponent(response.session)}`, {
          method: "DELETE",
        }).catch(() => {});
        return;
      }
      session.current = response.session;
      setSearch(response);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const pick = async (offer: string) => {
    if (!search) return;
    setBusy(true);
    setError("");
    try {
      const response = await apiFetch<CheckResult>(
        `/plugin-tests/subtitles/${encodeURIComponent(search.session)}/pick`,
        {
          method: "POST",
          body: JSON.stringify({ offer }),
        },
      );
      session.current = null;
      setResult(response);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
      setSearch(null);
    }
  };
  return (
    <Drawer open onClose={onClose} title={t("Check subtitles")} description={provider.name}>
      <div className="space-y-5">
        <p className="text-sm text-fg-muted">
          {t(
            "Search a stored title, then choose one subtitle to check its download and timed text. Test files are removed afterward; viewer selections are unchanged.",
          )}
        </p>
        {title ? (
          <div className="flex items-center justify-between gap-3 rounded-xl border border-border p-3">
            <p className="text-sm font-medium">
              {title.title} {title.year ? `(${title.year})` : ""}
            </p>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setTitle(null);
                setSearch(null);
                setResult(null);
                void cleanup();
              }}
            >
              {t("Change title")}
            </Button>
          </div>
        ) : (
          <TitleSearch
            label={t("Test title")}
            onPick={(value) => {
              setTitle(value);
              setSearch(null);
              setResult(null);
              void cleanup();
            }}
          />
        )}
        <Field label={t("Language")} htmlFor="subtitle-check-language">
          <SearchSelect
            value={language}
            onChange={setLanguage}
            options={languages}
            ariaLabel={t("Language")}
            searchable
            {...searchSelectLabels()}
          />
        </Field>
        <Button disabled={busy || !title || !language.trim()} onClick={() => void find()}>
          {busy ? t("Checking…") : t("Search subtitles")}
        </Button>
        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}
        {search ? (
          <section className="space-y-2" aria-label={t("Subtitle offers")}>
            <h3 className="font-medium">{t("Available subtitles")}</h3>
            <p className="text-sm text-fg-muted">
              {t("No subtitle has been downloaded. Choose one to continue.")}
            </p>
            {search.offers.length === 0 ? (
              <p>{t("This provider returned no subtitles for this title and language.")}</p>
            ) : (
              search.offers.map((offer) => (
                <div
                  key={offer.id}
                  className="flex items-center justify-between gap-3 rounded-xl border border-border p-3"
                >
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{offer.name}</p>
                    <p className="text-xs text-fg-muted">
                      {offer.provider} · {offer.language}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => void pick(offer.id)}
                  >
                    {t("Check download")}
                  </Button>
                </div>
              ))
            )}
          </section>
        ) : null}
        {result ? (
          <section
            role="status"
            className="space-y-2 rounded-xl border border-success/30 bg-success/5 p-4"
          >
            <h3 className="font-medium">{t("Subtitle downloaded and parsed")}</h3>
            <p className="text-sm">
              {t("{cues} timed cues · {bytes} bytes · {format}", {
                cues: result.cues,
                bytes: result.bytes,
                format: result.format.toUpperCase(),
              })}
            </p>
            <p className="text-sm text-fg-muted">
              {t("Test files removed. Playback rendering has not been checked.")}
            </p>
          </section>
        ) : null}
      </div>
    </Drawer>
  );
}
