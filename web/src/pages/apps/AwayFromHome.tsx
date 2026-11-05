import { SettingsSection, Spinner, toast } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { MoreOptions } from "../../components/ConfigSection";
import { ErrorPanel } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { t } from "../../i18n";
import { ApiError, api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { SchemaField, type FieldDef } from "../settings/SchemaForm";
import * as schema from "../settings/schema";
import { translatePlexFieldError, validateCustomUrls } from "./plex-account";

type Obj = Record<string, unknown>;

function group(id: string) {
  const found = schema.AWAY.find((item) => item.id === id);
  if (!found) throw new Error(`missing away settings group ${id}`);
  return found;
}

/**
 * Public address, port, extra URLs and certificate every app uses to reach
 * this server from outside. Stored under plex.*, but not Plex-only.
 */
export function AwayFromHome() {
  const config = useAsync(() => api.getConfig(), []);
  const address = group("address");
  const certificate = group("certificate");
  const keys = useMemo(
    () => [...(address.more ?? []), ...(certificate.more ?? [])].map((f) => f.key),
    [address, certificate],
  );
  const saved = useMemo<Obj | null>(() => {
    const plex = config.data?.plex as Obj | undefined;
    return plex ? Object.fromEntries(keys.map((key) => [key, plex[key]])) : null;
  }, [config.data, keys]);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<unknown>();
  const [saving, setSaving] = useState(false);
  const urlError = validateCustomUrls(draft?.customUrls);
  const shown = urlError ? { ...errors, "plex.customUrls": urlError } : errors;

  async function save() {
    if (!draft) return;
    if (urlError) {
      const fields = [{ field: "plex.customUrls", message: urlError }];
      setErrors({ "plex.customUrls": urlError });
      setError(new ApiError(400, "validation", t("Please fix the highlighted fields"), fields));
      return;
    }
    setSaving(true);
    setError(undefined);
    setErrors({});
    try {
      await api.putSection("plex", draft);
      toast.success(t("Settings saved"), { description: t("Away from home") });
      await config.reload();
    } catch (err) {
      if (err instanceof ApiError && err.fields) {
        const fields = err.fields.map((f) => ({
          field: f.field,
          message: translatePlexFieldError(f),
        }));
        setErrors(Object.fromEntries(fields.map((f) => [f.field, f.message])));
        setError(new ApiError(err.status, err.code, err.message, fields));
      } else setError(err);
    } finally {
      setSaving(false);
    }
  }

  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!draft)
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading settings")} />
      </div>
    );
  const fields = (list: FieldDef[] | undefined) =>
    (list ?? []).map((field) => (
      <SchemaField
        key={field.key}
        def={field}
        value={draft[field.key]}
        path={`plex.${field.key}`}
        error={shown[`plex.${field.key}`]}
        errors={shown}
        onChange={(next) => setDraft({ ...draft, [field.key]: next })}
      />
    ));
  const hasError = (list: FieldDef[] | undefined) =>
    (list ?? []).some((f) => Boolean(shown[`plex.${f.key}`]));
  return (
    <SettingsSection title={address.title} description={address.description}>
      <MoreOptions
        setting={address.id}
        label={t("More options: {title}", { title: address.title ?? "" })}
        text={t("Address and port")}
        hasError={hasError(address.more)}
      >
        {fields(address.more)}
      </MoreOptions>
      <MoreOptions
        setting={certificate.id}
        label={t("Use your own certificate")}
        text={t("Use your own certificate")}
        hasError={hasError(certificate.more)}
      >
        {fields(certificate.more)}
      </MoreOptions>
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setErrors({});
          setError(undefined);
        }}
      />
    </SettingsSection>
  );
}
