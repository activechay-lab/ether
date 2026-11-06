import { Artwork } from "./Artwork";
import type { NowPlaying } from "../lib/health";
import { initials } from "../lib/format";
import { useCallback, useState } from "react";

/** The logo response tells the console which tile keeps the ink legible. */
export const LOGO_BACKGROUND_HEADER = "X-Ether-Logo-Background";

/** The selected channel is independent of the programme's catalogue artwork. */
export function SessionArtwork({
  s,
  poster,
  className,
  posterTitle,
}: {
  s: NowPlaying;
  poster?: string;
  className: string;
  posterTitle?: string;
}) {
  if (!s.isLive)
    return <Artwork src={poster} alt={s.title} title={posterTitle} className={className} />;
  return <LiveArtwork key={s.channelId} s={s} className={className} />;
}

function LiveArtwork({ s, className }: { s: NowPlaying; className: string }) {
  const [lightFor, setLightFor] = useState<string>();
  const name = s.channelName || s.channelNumber || s.title;
  const src = s.channelId
    ? `/ether/api/livetv/channels/${encodeURIComponent(s.channelId)}/logo`
    : undefined;
  // The server scores the logo's ink against both tiles. A missing header, a
  // plain <img> load (no response headers) or a fetch failure keeps the dark
  // default, which is what an unreadable logo should look like.
  const onImageResponse = useCallback(
    (response: Response) => {
      if (src && response.headers.get(LOGO_BACKGROUND_HEADER) === "light") setLightFor(src);
    },
    [src],
  );
  return (
    <Artwork
      src={src}
      alt={name}
      title=""
      className={className}
      imgClassName={`object-contain p-2 ${src && lightFor === src ? "bg-white" : "bg-zinc-900"}`}
      icon={<span>{initials(name)}</span>}
      onImageResponse={onImageResponse}
    />
  );
}
