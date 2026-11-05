import { Alert, AlertDescription, AlertTitle, Spinner, cn } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, ChevronRight, Film } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Artwork } from "../../components/Artwork";
import { t } from "../../i18n";
import { mediaUrl, type Preroll } from "./prerolls-api";
import { videoState, type PlainError } from "./prerolls-copy";

/** A collapsed group of rarely needed controls. */
export function Disclosure({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 rounded-md text-sm font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <ChevronRight
          size={14}
          aria-hidden
          className={cn("transition-transform", open && "rotate-90")}
        />
        {label}
      </button>
      {open ? (
        <div
          id={id}
          className="mt-3 space-y-4 rounded-lg border border-border-subtle bg-bg-subtle p-3"
        >
          {children}
        </div>
      ) : null}
    </div>
  );
}

/** A request that failed, in plain words, with the server's detail beneath. */
export function ErrorNote({ error }: { error: PlainError | null }) {
  if (!error) return null;
  return (
    <Alert variant="destructive" role="alert">
      <AlertTitle>{error.message}</AlertTitle>
      {error.detail ? <AlertDescription>{error.detail}</AlertDescription> : null}
    </Alert>
  );
}

/** A pre-roll's 16:9 thumbnail with its converting or failed state. */
export function PrerollThumb({
  preroll,
  className,
  eager,
}: {
  preroll: Pick<Preroll, "name" | "video">;
  className?: string;
  eager?: boolean;
}) {
  const state = videoState(preroll);
  return (
    <div
      className={cn(
        "relative aspect-video shrink-0 overflow-hidden rounded-md bg-bg-subtle ring-1 ring-border",
        className,
      )}
    >
      {state === "ready" && preroll.video.thumbUrl ? (
        <Artwork
          src={mediaUrl(preroll.video.thumbUrl)}
          alt=""
          title={preroll.name}
          eager={eager}
          icon={<Film size={18} />}
          className="size-full"
          imgClassName="size-full object-cover"
        />
      ) : (
        <div className="grid size-full place-items-center text-fg-subtle">
          {state === "processing" ? (
            <Spinner size={16} label={t("Converting video")} />
          ) : state === "failed" ? (
            <AlertTriangle size={18} className="text-danger" aria-hidden />
          ) : (
            <Film size={18} aria-hidden />
          )}
        </div>
      )}
    </div>
  );
}
