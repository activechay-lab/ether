import { Button, Input } from "@braedonsaunders/appkit-ui";
import { ArrowUp, Folder, ChevronRight } from "lucide-react";
import { Fragment, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { MutationError } from "../../components/feedback";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
type Listing = {
  breadcrumbs: { name: string; path: string }[];
  path: string;
  parent: string;
  directories: string[];
  directoryPosters?: Record<string, number>;
  posters: number;
  limited: boolean;
};
export function PosterFolder({
  value,
  save,
  disabled = false,
}: {
  value: string;
  disabled?: boolean;
  save: (path: string) => Promise<void>;
}) {
  const [listing, setListing] = useState<Listing | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  async function browse(path: string) {
    setBusy(true);
    setError(null);
    try {
      setListing(await apiFetch<Listing>("/poster-folders?path=" + encodeURIComponent(path)));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  async function choose(path: string) {
    setBusy(true);
    setError(null);
    try {
      await save(path);
      setOpen(false);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <div className="min-w-40 flex-1">
          <Input
            aria-label={t("Poster folder")}
            readOnly
            value={value}
            placeholder={t("Choose folder")}
          />
        </div>
        <Button
          size="sm"
          variant="outline"
          disabled={disabled || busy}
          onClick={() => {
            setOpen(true);
            void browse(value || "");
          }}
        >
          {t("Change")}
        </Button>
        {value && (
          <Button
            size="sm"
            variant="ghost"
            disabled={disabled || busy}
            onClick={() => void choose("")}
          >
            {t("Remove")}
          </Button>
        )}
      </div>
      {!open && error ? <MutationError error={error} /> : null}
      <Drawer
        open={open}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
        title={t("Choose folder")}
        footer={
          <div className="flex w-full justify-end gap-2">
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => setOpen(false)}>
              {t("Cancel")}
            </Button>
            <Button
              size="sm"
              disabled={disabled || busy || !listing || !!error}
              onClick={() => {
                if (listing) void choose(listing.path);
              }}
            >
              {t("Choose this folder")}
            </Button>
          </div>
        }
      >
        <div className="space-y-4">
          {error ? <MutationError error={error} /> : null}
          {listing && (
            <>
              <div className="flex items-center gap-3">
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t("Up")}
                  title={t("Up")}
                  disabled={disabled || busy || listing.path === listing.parent}
                  onClick={() => void browse(listing.parent)}
                >
                  <ArrowUp size={16} />
                </Button>
                <nav
                  aria-label={t("Folders")}
                  className="flex min-w-0 flex-wrap items-center gap-2 text-sm"
                >
                  {listing.breadcrumbs.map((crumb, index) => (
                    <Fragment key={crumb.path}>
                      {index > 0 && (
                        <span className="text-fg-subtle" aria-hidden>
                          /
                        </span>
                      )}
                      <button
                        type="button"
                        className="text-fg-muted hover:text-fg hover:underline disabled:opacity-50"
                        disabled={disabled || busy}
                        onClick={() => void browse(crumb.path)}
                      >
                        {index === 0 ? t("Folders") : crumb.name}
                      </button>
                    </Fragment>
                  ))}
                </nav>
              </div>
              <div className="rounded-lg border border-border bg-bg-subtle px-3 py-2 text-sm text-fg-muted break-all">
                {listing.path}
              </div>
              <p className="text-xs text-fg-muted">
                {t("Found {count} posters", { count: listing.posters })}
                {listing.limited ? "+" : ""}
              </p>
              <div className="max-h-96 overflow-auto divide-y divide-border rounded-xl border border-border">
                {listing.directories.map((name) => (
                  <button
                    type="button"
                    key={name}
                    disabled={disabled || busy}
                    className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-fg hover:bg-bg-subtle disabled:opacity-50"
                    onClick={() => void browse(listing.path.replace(/[\\/]$/, "") + "/" + name)}
                  >
                    <Folder size={18} className="shrink-0 text-fg-muted" />
                    <span className="min-w-0 flex-1 break-words">{name}</span>
                    {listing.directoryPosters?.[name] !== undefined && (
                      <span
                        className="text-xs text-fg-muted"
                        aria-label={t("Found {count} posters", {
                          count: listing.directoryPosters[name],
                        })}
                      >
                        {listing.directoryPosters[name]}
                      </span>
                    )}
                    <ChevronRight size={16} className="shrink-0 text-fg-subtle" />
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      </Drawer>
    </div>
  );
}
