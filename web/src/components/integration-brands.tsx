import postersplus from "../assets/brands/postersplus.png?no-inline";
import xrdb from "../assets/brands/xrdb.png?no-inline";
import betterposters from "../assets/brands/betterposters.png?no-inline";
import rpdb from "../assets/brands/rpdb.jpg?no-inline";
import { Cast, FolderOpen, ListVideo } from "lucide-react";
import type { ReactNode } from "react";
import emby from "../assets/brands/emby.svg?no-inline";
import jellyfin from "../assets/brands/jellyfin.svg?no-inline";
import plex from "../assets/brands/plex.svg?no-inline";
import silo from "../assets/brands/silo.png?no-inline";
import simkl from "../assets/brands/simkl.svg?no-inline";
import tmdb from "../assets/brands/themoviedatabase.svg?no-inline";
import trakt from "../assets/brands/trakt.svg?no-inline";

/**
 * Real vendored marks for IntegrationAppCard's `logo` slot.
 * The card does not choose one from `identity.id`; the caller passes it.
 * Color follows the surrounding text (`currentColor` via a mask).
 */
function BrandMark({ src, color }: { src: string; color: string }) {
  return (
    <span
      aria-hidden
      className="block size-7 bg-current"
      style={{
        color,
        maskImage: `url("${src}")`,
        maskPosition: "center",
        maskSize: "contain",
        maskRepeat: "no-repeat",
        WebkitMaskImage: `url("${src}")`,
        WebkitMaskPosition: "center",
        WebkitMaskSize: "contain",
        WebkitMaskRepeat: "no-repeat",
      }}
    />
  );
}

export function PlexMark() {
  return <BrandMark src={plex} color="#e5a00d" />;
}
export function JellyfinMark() {
  return <BrandMark src={jellyfin} color="#aa5cc3" />;
}
export function EmbyMark() {
  return <BrandMark src={emby} color="#52b54b" />;
}
/** Silo's own full-colour app icon, so it is an image rather than a mask. */
export function SiloMark() {
  return <img src={silo} alt="" aria-hidden className="block size-7 rounded-md" />;
}

/** DLNA, Files, and M3U have no vendored mark. These are icons, not letter tiles. */
export function householdIcon(id: string): ReactNode | undefined {
  switch (id) {
    case "dlna":
      return <Cast size={22} aria-hidden />;
    case "webdav":
      return <FolderOpen size={22} aria-hidden />;
    case "xtream":
    case "m3u":
      return <ListVideo size={22} aria-hidden />;
    default:
      return undefined;
  }
}
export function SimklMark() {
  return <BrandMark src={simkl} color="currentColor" />;
}
export function TmdbMark() {
  return <BrandMark src={tmdb} color="#01b4e4" />;
}
export function TraktMark() {
  return <BrandMark src={trakt} color="#ed1c24" />;
}

/** Distinct poster-service symbols; Custom deliberately uses a neutral frame. */
export function IntegrationMark({ id }: { id: string }) {
  if (id === "tmdb") return <TmdbMark />;
  if (id === "trakt") return <TraktMark />;
  if (id === "simkl") return <SimklMark />;
  if (id === "plexservers") return <PlexMark />;
  const posters: Record<string, string> = { postersplus, xrdb, betterposters, rpdb };
  if (posters[id])
    return (
      <img
        src={posters[id]}
        alt=""
        aria-hidden
        className={
          id === "xrdb" || id === "postersplus"
            ? "block size-8 rounded bg-fg dark:bg-bg-subtle p-1 object-contain"
            : "block size-8 object-contain"
        }
      />
    );
  return (
    <svg
      aria-hidden
      viewBox="0 0 32 32"
      className="size-8"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {id === "llm" ? (
        <>
          <path d="m16 3 3.5 9.5L29 16l-9.5 3.5L16 29l-3.5-9.5L3 16l9.5-3.5Z" />
        </>
      ) : id === "introdb" ? (
        <>
          <circle cx="16" cy="16" r="13" />
          <path d="m12 10 9 6-9 6Zm11 0v12" />
        </>
      ) : id === "ratings" ? (
        <>
          <path d="m16 3 4 8 9 1-6.5 6.5L24 28l-8-4-8 4 1.5-9.5L3 12l9-1Z" />
        </>
      ) : id === "opensubtitles" ? (
        <>
          <rect x="3" y="5" width="26" height="22" rx="4" />
          <path d="M8 15h6m4 0h6M8 21h16" />
        </>
      ) : (
        <>
          <rect x="5" y="3" width="22" height="26" rx="3" />
          <circle cx="12" cy="11" r="2" />
          <path d="m6 24 7-8 5 5 5-6 4 4" />
        </>
      )}
    </svg>
  );
}
