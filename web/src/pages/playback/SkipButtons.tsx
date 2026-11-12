import { Button, SettingsSection, Switch } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useApp } from "../../app-context";
import { secretConfigured } from "../../components/IntegrationAccount";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tx } from "../../i18n";
import { api } from "../../lib/api";
import { textOf, useRegistry } from "../../lib/extend";
import { useAsync } from "../../lib/hooks";

type Draft = {
  theIntroDb: boolean;
  skipDb: boolean;
  skipIntro: boolean;
  skipCredits: boolean;
  sourceOrder: string[];
};

/** Intro and credits skip buttons, plus the named places those times come from. */
export function SkipButtons({ hidden = false }: { hidden?: boolean }) {
  const { refreshDashboard } = useApp();
  const config = useAsync(() => api.getConfig(), []);
  const cfg = config.data;
  const registry = useRegistry("markerSource");
  const saved = useMemo<Draft | null>(() => {
    if (!cfg) return null;
    const markers = cfg.markers;
    return {
      theIntroDb: Boolean(markers?.theIntroDb),
      skipDb: Boolean(markers?.skipDb),
      skipIntro: markers?.skipIntro !== false,
      skipCredits: markers?.skipCredits !== false,
      sourceOrder: markers?.sourceOrder ?? ["chapter", "theintrodb"],
    };
  }, [cfg]);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const apiKey = cfg?.markers?.apiKey;

  async function submit() {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      await api.putSection("markers", draft);
      await config.reload();
      void refreshDashboard();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  const saveBar = (
    <SaveBar
      dirty={dirty}
      saving={saving}
      error={error}
      onSave={() => void submit()}
      onDiscard={() => {
        reset();
        setError(null);
      }}
    />
  );
  if (hidden) return saveBar;
  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!draft) return <LoadingRows rows={3} />;

  return (
    <>
      <SettingsSection
        title={t("Skip buttons")}
        description={t("Each switch turns that button on or off. The app still has to show it.")}
      >
        <SettingsRow
          setting="skip-intro"
          title={t("Skip intro")}
          description={t("Shows a button to skip the opening and a recap.")}
          control={
            <Switch
              checked={draft.skipIntro}
              aria-label={t("Skip intro")}
              onChange={(e) => setDraft({ ...draft, skipIntro: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          setting="skip-credits"
          title={t("Skip credits")}
          description={t("Shows a button to skip the credits.")}
          control={
            <Switch
              checked={draft.skipCredits}
              aria-label={t("Skip credits")}
              onChange={(e) => setDraft({ ...draft, skipCredits: e.currentTarget.checked })}
            />
          }
        />
      </SettingsSection>
      <SettingsSection title={t("Where the times come from")}>
        <SettingsRow
          title={t("Intro and credit times from IntroDB.")}
          description={
            <>
              {t("Ether asks IntroDB once per episode and remembers the answer.")}{" "}
              {secretConfigured(apiKey)
                ? null
                : tx("Add an IntroDB key in {link}.", {
                    link: (
                      <Link to="/services/introdb" className="text-primary underline">
                        {t("Integrations")}
                      </Link>
                    ),
                  })}
            </>
          }
          control={
            <Switch
              checked={draft.theIntroDb}
              aria-label={t("Look up IntroDB")}
              onChange={(e) => setDraft({ ...draft, theIntroDb: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Intro and credit times from SkipDB.")}
          description={t(
            "Ether asks the open SkipDB lookup once per episode and remembers the answer.",
          )}
          control={
            <Switch
              checked={draft.skipDb}
              aria-label={t("Look up SkipDB")}
              onChange={(e) => setDraft({ ...draft, skipDb: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Chapter titles")}
          description={t(
            "A chapter named intro, opening, credits, or ending supplies the skip range.",
          )}
        />
        <SettingsRow
          title={t("Subtitle dialogue")}
          description={t(
            "Dialogue in a downloaded subtitle keeps a scene in the credits from being skipped.",
          )}
        />
      </SettingsSection>
      {registry.descriptors.some((d) => d.owner !== "builtin") || draft.skipDb ? (
        <SettingsSection
          title={t("Source order")}
          description={t("The first source with a matching range supplies the skip button.")}
        >
          {[
            ...draft.sourceOrder,
            ...(draft.skipDb && !draft.sourceOrder.includes("skipdb") ? ["skipdb"] : []),
            ...registry.descriptors
              .filter((d) => d.owner !== "builtin" && !draft.sourceOrder.includes(d.id))
              .map((d) => d.id),
          ].map((id) => {
            const index = draft.sourceOrder.indexOf(id);
            const descriptor = registry.descriptors.find((d) => d.id === id);
            const label =
              id === "chapter"
                ? t("Chapter titles")
                : id === "theintrodb"
                  ? t("IntroDB")
                  : id === "skipdb"
                    ? t("SkipDB")
                    : textOf(descriptor?.label) || id;
            return (
              <SettingsRow
                key={id}
                title={label}
                control={
                  <div className="flex items-center gap-2">
                    {id.startsWith("plugin:") ? (
                      <Switch
                        checked={index >= 0}
                        aria-label={label}
                        onChange={(e) =>
                          setDraft({
                            ...draft,
                            sourceOrder: e.currentTarget.checked
                              ? [...draft.sourceOrder, id]
                              : draft.sourceOrder.filter((entry) => entry !== id),
                          })
                        }
                      />
                    ) : null}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={index <= 0}
                      aria-label={t("Move up")}
                      onClick={() => {
                        const order = [...draft.sourceOrder];
                        [order[index - 1], order[index]] = [order[index]!, order[index - 1]!];
                        setDraft({ ...draft, sourceOrder: order });
                      }}
                    >
                      ↑
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={index < 0 || index === draft.sourceOrder.length - 1}
                      aria-label={t("Move down")}
                      onClick={() => {
                        const order = [...draft.sourceOrder];
                        [order[index + 1], order[index]] = [order[index]!, order[index + 1]!];
                        setDraft({ ...draft, sourceOrder: order });
                      }}
                    >
                      ↓
                    </Button>
                  </div>
                }
              />
            );
          })}
        </SettingsSection>
      ) : null}
      {saveBar}
    </>
  );
}
