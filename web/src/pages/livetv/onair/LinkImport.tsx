import { useEffect, useState } from "react";
import { Badge, Button, Select, Textarea } from "@braedonsaunders/appkit-ui";
import { CheckCircle2, Download, Link, RefreshCw } from "lucide-react";
import { t } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { onair, type DownloaderStatus } from "./model";

/** Shared import action placed beside uploads in Reels and Library. */
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
  const [installing, setInstalling] = useState(false);
  const [status, setStatus] = useState<DownloaderStatus | null>(null);
  const [error, setError] = useState("");

  const isReel = Boolean(reelId || ensureReel);
  const needsDownloader = isReel || kind === "video" || kind === "audio";

  useEffect(() => {
    if (!open) return;
    let active = true;
    void onair
      .downloaderStatus()
      .then((s) => {
        if (active) setStatus(s);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [open]);

  const handleInstall = async (update: boolean) => {
    setInstalling(true);
    setError("");
    try {
      const next = await onair.installDownloader(update);
      setStatus(next);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setInstalling(false);
    }
  };

  return (
    <div className="space-y-3">
      <Button size="sm" variant="outline" onClick={() => setOpen(!open)}>
        <Link size={14} />
        {t("Add from link")}
      </Button>
      {open ? (
        <div className="space-y-3 rounded-xl border border-border bg-surface p-3.5 shadow-sm">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p className="text-xs font-medium text-fg">
                {isReel
                  ? t("Import video clips from a link")
                  : t("Import station media from a link")}
              </p>
              <p className="text-xs text-fg-muted">
                {isReel
                  ? t(
                      "Paste a YouTube, Vimeo, Archive.org or direct video link · up to 3 min and 500 MB each",
                    )
                  : t(
                      "Paste a YouTube, Vimeo, Archive.org or direct media link · up to 30 s and 32 MB each",
                    )}
              </p>
            </div>
            {needsDownloader && status ? (
              <div className="flex flex-wrap items-center gap-2">
                {status.available ? (
                  <>
                    <Badge variant="success">
                      <span className="inline-flex items-center gap-1">
                        <CheckCircle2 size={12} />
                        {status.version
                          ? t("yt-dlp {version} ready", { version: status.version })
                          : t("yt-dlp ready")}
                      </span>
                    </Badge>
                    {status.canUpdate ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={installing}
                        onClick={() => void handleInstall(true)}
                      >
                        <RefreshCw size={13} className={installing ? "animate-spin" : ""} />
                        {installing ? t("Updating yt-dlp…") : t("Update yt-dlp")}
                      </Button>
                    ) : null}
                  </>
                ) : status.canInstall ? (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={installing}
                    onClick={() => void handleInstall(false)}
                  >
                    <Download size={14} />
                    {installing ? t("Installing yt-dlp…") : t("Install yt-dlp")}
                  </Button>
                ) : null}
              </div>
            ) : null}
          </div>
          {needsDownloader && status && !status.available && status.canInstall ? (
            <p className="rounded-lg bg-bg-subtle px-3 py-2 text-xs text-fg-muted">
              {t(
                "Install the official verified yt-dlp tool with one click to import from YouTube and hundreds of video sites. Direct MP4 and Archive.org links work immediately.",
              )}
            </p>
          ) : null}
          <Textarea
            className="w-full"
            rows={3}
            aria-label={t("Media links")}
            placeholder={t("Paste one link per line (https://www.youtube.com/watch?v=…)")}
            value={links}
            onChange={(e) => setLinks(e.currentTarget.value)}
          />
          {!isReel ? (
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
          <div className="flex items-center gap-2">
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
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setOpen(false);
                setError("");
              }}
            >
              {t("Cancel")}
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
