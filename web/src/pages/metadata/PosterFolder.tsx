import { Button } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { MutationError } from "../../components/feedback";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
type Listing = {
  breadcrumbs: { name: string; path: string }[];
  path: string;
  parent: string;
  directories: string[];
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
  return (
    <div className="space-y-3 text-sm">
      {value && <p className="break-all">{value}</p>}
      <div className="flex flex-wrap gap-2">
        <Button
          disabled={disabled || busy}
          onClick={() => {
            setOpen(true);
            void browse(value || "");
          }}
        >
          {t("Choose folder")}
        </Button>
        {value && (
          <Button
            variant="outline"
            disabled={disabled || busy}
            onClick={async () => {
              setBusy(true);
              try {
                await save("");
              } catch (e) {
                setError(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Remove")}
          </Button>
        )}
      </div>
      {!open && error ? <MutationError error={error} /> : null}
      <Drawer open={open} onClose={() => setOpen(false)} title={t("Choose folder")}>
        <div className="space-y-3 p-5">
          {error ? <MutationError error={error} /> : null}
          {listing && (
            <>
              <nav aria-label={t("Folders")} className="flex flex-wrap gap-2">
                {listing.breadcrumbs.map((crumb) => (
                  <Button
                    key={crumb.path}
                    disabled={disabled || busy}
                    onClick={() => void browse(crumb.path)}
                  >
                    {crumb.name}
                  </Button>
                ))}
              </nav>
              <Button
                disabled={disabled || busy || listing.path === listing.parent}
                onClick={() => void browse(listing.parent)}
              >
                {t("Up")}
              </Button>
              <p>
                {t("Found {count} posters", { count: listing.posters })}
                {listing.limited ? "+" : ""}
              </p>
              <div className="max-h-80 overflow-auto flex flex-col gap-1">
                {listing.directories.map((name) => (
                  <Button
                    key={name}
                    variant="ghost"
                    className="justify-start text-left"
                    disabled={disabled || busy}
                    onClick={() => void browse(listing.path.replace(/[\\/]$/, "") + "/" + name)}
                  >
                    {name}
                  </Button>
                ))}
              </div>
              <Button
                disabled={disabled || busy || !!error}
                onClick={async () => {
                  setBusy(true);
                  try {
                    await save(listing.path);
                    setOpen(false);
                  } catch (e) {
                    setError(e);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {t("Choose this folder")}
              </Button>
            </>
          )}
        </div>
      </Drawer>
    </div>
  );
}
