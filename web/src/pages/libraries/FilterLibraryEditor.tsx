import { Button, toast } from "@braedonsaunders/appkit-ui";
import { ArrowLeft, ChevronRight, Film, Plug } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { t, tx } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library, type PluginLibrarySource } from "./api";
import { CatalogFilterBar, compileQuery, readQuery } from "./browser/CatalogFilterBar";
import { SetupWizard } from "./SetupWizard";

/**
 * Create a library from the Libraries page: first choose where titles come
 * from (TMDB saved filter or an installed content-kind plugin), then either
 * build the saved filter or walk the plugin's setup wizard.
 */
export function NewLibraryDrawer({
  onClose,
  onCreated,
  onPluginOpened,
}: {
  onClose: () => void;
  onCreated: (lib: Library) => void;
  onPluginOpened: (libId?: number) => void;
}) {
  const [step, setStep] = useState<"source" | "tmdb" | { pluginId: string; libId?: number }>(
    "source",
  );
  const [busyPlugin, setBusyPlugin] = useState<string | null>(null);
  const pluginSources = useAsync((signal) => librariesApi.listPluginSources(signal), []);

  if (step === "tmdb") {
    return (
      <FilterLibraryEditor
        onBack={() => setStep("source")}
        onClose={onClose}
        onCreated={onCreated}
      />
    );
  }

  if (typeof step === "object") {
    return (
      <SetupWizard
        pluginId={step.pluginId}
        onClose={() => onPluginOpened(step.libId)}
      />
    );
  }

  const pickPlugin = async (source: PluginLibrarySource) => {
    setBusyPlugin(source.pluginId);
    try {
      const setup = await librariesApi.ensurePluginSetup(source.pluginId);
      const libId = setup.libraries[0]?.id;
      if (!setup.steps.length) {
        toast.success(t("Library ready"));
        onPluginOpened(libId);
        return;
      }
      setStep({ pluginId: source.pluginId, libId });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusyPlugin(null);
    }
  };

  const sources = pluginSources.data ?? [];

  return (
    <Drawer
      open
      onClose={onClose}
      title={t("New library")}
      size="lg"
      footer={
        <div className="flex justify-end">
          <Button variant="ghost" onClick={onClose}>
            {t("Cancel")}
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-medium text-fg">{t("Where do titles come from?")}</p>
        <div className="space-y-2">
          <button
            type="button"
            onClick={() => setStep("tmdb")}
            className="flex w-full items-center gap-3 rounded-xl border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong hover:bg-surface-hover"
          >
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-surface-alt text-primary">
              <Film size={18} aria-hidden />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block font-medium text-fg">
                {t("Movies or TV from TMDB (a saved filter)")}
              </span>
              <span className="block text-sm text-fg-muted">
                {t("Pick genres, years, ratings, or keywords from your TMDB catalog.")}
              </span>
            </span>
            <ChevronRight size={16} className="shrink-0 text-fg-muted" aria-hidden />
          </button>

          {pluginSources.loading && !pluginSources.data ? <LoadingRows rows={2} /> : null}
          {pluginSources.error ? (
            <ErrorPanel error={pluginSources.error} onRetry={() => void pluginSources.reload()} />
          ) : null}

          {sources.map((src) => {
            const kindLabel =
              src.kinds.map((k) => k.library?.name || k.label).filter(Boolean).join(", ") ||
              src.pluginName;
            return (
              <button
                key={src.pluginId}
                type="button"
                disabled={busyPlugin !== null}
                onClick={() => void pickPlugin(src)}
                className="flex w-full items-center gap-3 rounded-xl border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong hover:bg-surface-hover disabled:opacity-60"
              >
                <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-surface-alt text-primary">
                  <Plug size={18} aria-hidden />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block font-medium text-fg">{kindLabel}</span>
                  <span className="block text-sm text-fg-muted">
                    {src.summary
                      ? `${t("From {plugin}", { plugin: src.pluginName })} · ${src.summary}`
                      : t("From {plugin}", { plugin: src.pluginName })}
                  </span>
                </span>
                <ChevronRight size={16} className="shrink-0 text-fg-muted" aria-hidden />
              </button>
            );
          })}
        </div>

        {!pluginSources.loading && sources.length === 0 ? (
          <p className="text-sm text-fg-muted">
            {tx("Want books, audiobooks, comics, or music? {link} that provides a library.", {
              link: (
                <Link to="/plugins" onClick={onClose} className="font-medium text-primary underline">
                  {t("Install a plugin")}
                </Link>
              ),
            })}
          </p>
        ) : null}
      </div>
    </Drawer>
  );
}

/**
 * Create (or preview) a saved-filter library. The filter itself is
 * built with the same CatalogFilterBar the browser uses, seeded from
 * the current browse state when the owner asks from a library page.
 * New libraries are console-only until "Show in apps" is turned on
 * (§15.3). A saved filter cannot be edited after creation yet — the
 * libraries service has no SetFilter — so this drawer is create-only
 * and says so before saving.
 */
export function FilterLibraryEditor({
  seed,
  onBack,
  onClose,
  onCreated,
}: {
  seed?: URLSearchParams;
  onBack?: () => void;
  onClose: () => void;
  onCreated: (lib: Library) => void;
}) {
  const viewers = useAsync(() => api.users(), []);
  const [name, setName] = useState("");
  const [type, setType] = useState(seed?.get("type") === "show" ? "show" : "movie");
  const [params, setParams] = useState<URLSearchParams>(
    () => new URLSearchParams(seed?.toString() ?? ""),
  );
  const [saving, setSaving] = useState(false);

  const buildFilter = (): Record<string, unknown> => {
    const compiled = compileQuery(
      readQuery(params.get("filters") || ""),
      params.get("viewer") || "",
    ) as Record<string, unknown>;
    const filter: Record<string, unknown> = { ...compiled, type };
    const genre = params.get("genre");
    if (genre) filter.genres = [genre];
    const decade = Number(params.get("decade"));
    if (decade) {
      filter.yearFrom = decade;
      filter.yearTo = decade + 9;
    }
    const q = (params.get("q") || "").trim();
    if (q) filter.query = q;
    return filter;
  };

  const save = async () => {
    setSaving(true);
    try {
      const lib = await librariesApi.createFilter(name.trim(), buildFilter());
      toast.success(t("Library created"));
      onCreated(lib);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      open
      onClose={onClose}
      title={t("New library from a filter")}
      size="lg"
      footer={
        <div className="flex items-center justify-between gap-3">
          {onBack ? (
            <Button variant="ghost" size="sm" onClick={onBack}>
              <ArrowLeft size={14} /> {t("Back")}
            </Button>
          ) : (
            <span className="text-xs text-fg-muted">
              {t("Starts console-only. Turn on “Show in apps” in its Settings when ready.")}
            </span>
          )}
          <span className="flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button disabled={saving || !name.trim()} onClick={() => void save()}>
              {t("Create library")}
            </Button>
          </span>
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <label className="library-field">
          <span>{t("Library name")}</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("Comedies the kids can watch")}
            aria-label={t("Library name")}
          />
        </label>
        <div className="library-field">
          <span>{t("What goes in it")}</span>
          <Segmented
            ariaLabel={t("What goes in it")}
            value={type}
            onChange={setType}
            options={[
              { value: "movie", label: t("Movies") },
              { value: "show", label: t("TV shows") },
            ]}
          />
        </div>
        <CatalogFilterBar
          params={params}
          viewers={viewers.data || []}
          onChange={(next) => setParams(new URLSearchParams(next.toString()))}
        />
        <p className="text-sm text-fg-muted">
          {t(
            "Pick filters above; every matching title joins the library and keeps its place in Movies or TV Shows too. The name can change later, but the filter itself cannot yet — you would delete and recreate the library.",
          )}
        </p>
      </div>
    </Drawer>
  );
}
