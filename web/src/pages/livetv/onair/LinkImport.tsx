import { useState } from "react";
import { Button, Select, Textarea } from "@braedonsaunders/appkit-ui";
import { Link } from "lucide-react";
import { t } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";

/** Shared import action; W15 can place it beside uploads in either library. */
export function LinkImport({
  reelId,
  onImported,
  ensureReel,
}: {
  reelId?: string;
  ensureReel?: () => Promise<string>;
  onImported: () => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [links, setLinks] = useState("");
  const [kind, setKind] = useState("video");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <div className="space-y-3">
      <Button size="sm" variant="outline" onClick={() => setOpen(!open)}>
        <Link size={14} />
        {t("Add from link")}
      </Button>
      {open ? (
        <div className="space-y-3 rounded-lg border border-border p-3">
          <Textarea
            className="w-full"
            rows={4}
            aria-label={t("Media links")}
            placeholder={t("Paste one link per line")}
            value={links}
            onChange={(e) => setLinks(e.currentTarget.value)}
          />
          {!reelId && !ensureReel ? (
            <Select
              aria-label={t("Asset type")}
              value={kind}
              onChange={(e) => setKind(e.currentTarget.value)}
            >
              <option value="video">{t("Video")}</option>
              <option value="audio">{t("Audio")}</option>
              <option value="image">{t("Image")}</option>
            </Select>
          ) : null}
          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}
          <Button
            size="sm"
            disabled={busy || !links.trim()}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                const targetReel = ensureReel ? await ensureReel() : reelId;
                await apiFetch(
                  `/onair/${targetReel ? `reels/${encodeURIComponent(targetReel)}/clips` : "assets"}/import`,
                  {
                    method: "POST",
                    body: JSON.stringify({
                      urls: links
                        .split(/\r?\n/)
                        .map((s) => s.trim())
                        .filter(Boolean),
                      kind,
                    }),
                  },
                );
                setLinks("");
                setOpen(false);
                await onImported();
              } catch (e) {
                setError(errorMessage(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? t("Importing…") : t("Import")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
