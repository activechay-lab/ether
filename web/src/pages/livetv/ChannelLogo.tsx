import { cn } from "@braedonsaunders/appkit-ui";
import { useCallback, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { LOGO_BACKGROUND_HEADER } from "../../components/SessionArtwork";
import { initials } from "../../lib/format";
import { hueOf } from "./model";

/** A channel logo or a generated monogram when it has none. */
export function ChannelLogo({
  name,
  logo,
  seed,
  className,
  size = 36,
  channelId,
}: {
  name: string;
  logo?: string;
  seed?: string;
  className?: string;
  size?: number;
  channelId?: string;
}) {
  const [failed, setFailed] = useState<string | null>(null);
  const [lightFor, setLightFor] = useState<string>();
  // The server proxies the saved logo and caches it per channel URL; the
  // version keeps an edited logo from showing the cached old one.
  const src =
    channelId && logo
      ? `/ether/api/livetv/channels/${encodeURIComponent(channelId)}/logo?v=${logoVersion(logo)}`
      : undefined;
  const onImageResponse = useCallback(
    (response: Response) => {
      if (src && response.headers.get(LOGO_BACKGROUND_HEADER) === "light") setLightFor(src);
    },
    [src],
  );
  const hue = hueOf(seed || name || "?");
  const showImg = logo && failed !== logo;
  const monogram = (
    <span
      className="font-semibold tracking-tight text-white drop-shadow-sm"
      style={{ fontSize: Math.max(10, Math.round(size * 0.36)) }}
    >
      {initials(name || "?")}
    </span>
  );
  return (
    <span
      className={cn(
        "relative grid shrink-0 place-items-center overflow-hidden rounded-lg ring-1 ring-inset ring-black/5 dark:ring-white/10",
        showImg && !src ? "bg-white dark:bg-white/90" : "",
        className,
      )}
      style={{
        width: size,
        height: size,
        ...(!showImg || src
          ? {
              background: `linear-gradient(135deg, hsl(${hue} 70% 52%), hsl(${(hue + 40) % 360} 75% 38%))`,
            }
          : null),
      }}
      aria-hidden
    >
      {src ? (
        <Artwork
          src={src}
          alt=""
          title=""
          className="size-full bg-transparent"
          imgClassName={`object-contain p-[12%] ${lightFor === src ? "bg-white" : "bg-zinc-900"}`}
          icon={monogram}
          onImageResponse={onImageResponse}
        />
      ) : showImg ? (
        <img
          src={logo}
          alt=""
          loading="lazy"
          className="size-full object-contain p-[12%]"
          onError={() => setFailed(logo)}
        />
      ) : (
        monogram
      )}
    </span>
  );
}

/** A short, stable fingerprint of a logo URL (which may be a long data: URI). */
function logoVersion(logo: string): string {
  let h = 2166136261;
  for (let i = 0; i < logo.length; i++) h = Math.imul(h ^ logo.charCodeAt(i), 16777619);
  return (h >>> 0).toString(36);
}
