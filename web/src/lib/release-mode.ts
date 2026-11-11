import type { AvailabilityConfig } from "./types";

export type ReleaseMode = "playable" | "digital" | "immediate";
export type ReleaseMediaType = "movie" | "show";

/** Map stored flags onto the three owner modes. */
export function releaseMode(cfg: AvailabilityConfig): ReleaseMode {
  if (cfg.mode === "digital" || cfg.mode === "immediate" || cfg.mode === "playable")
    return cfg.mode;
  if (cfg.disableMovieEvidenceGate) return "immediate";
  if (cfg.digitalDateEvidence) return "digital";
  return "playable";
}

/** Per-library release mode: movies have 3 modes; TV shows have playable or immediate. */
export function releaseModeFor(cfg: AvailabilityConfig, kind: ReleaseMediaType): ReleaseMode {
  if (kind === "show") {
    if (cfg.showMode === "immediate") return "immediate";
    if (cfg.showMode === "playable") return "playable";
    return releaseMode(cfg) === "immediate" ? "immediate" : "playable";
  }
  if (
    cfg.movieMode === "digital" ||
    cfg.movieMode === "immediate" ||
    cfg.movieMode === "playable"
  ) {
    return cfg.movieMode;
  }
  return releaseMode(cfg);
}

/** Write engine flags for a chosen mode. Gentle/Unlimited become self-tuned. */
export function applyReleaseMode(cfg: AvailabilityConfig, mode: ReleaseMode): AvailabilityConfig {
  const next = { ...cfg, mode };
  if (mode === "digital") {
    next.monitor = false;
    next.disableMovieEvidenceGate = false;
    next.digitalDateEvidence = true;
  } else if (mode === "immediate") {
    next.monitor = false;
    next.disableMovieEvidenceGate = true;
    next.digitalDateEvidence = false;
  } else {
    next.monitor = true;
    next.disableMovieEvidenceGate = false;
    next.digitalDateEvidence = false;
  }
  if (next.effort === "gentle" || next.effort === "unlimited") next.effort = "normal";
  return next;
}

/** Write per-library release mode while keeping derived engine flags in sync. */
export function applyReleaseModeFor(
  cfg: AvailabilityConfig,
  kind: ReleaseMediaType,
  mode: ReleaseMode,
): AvailabilityConfig {
  const movieMode: ReleaseMode = kind === "movie" ? mode : releaseModeFor(cfg, "movie");
  const showMode: ReleaseMode =
    kind === "show" ? (mode === "immediate" ? "immediate" : "playable") : releaseModeFor(cfg, "show");
  const next: AvailabilityConfig = {
    ...cfg,
    mode: movieMode,
    movieMode,
    showMode,
    monitor: movieMode === "playable" || showMode === "playable",
    disableMovieEvidenceGate: movieMode === "immediate",
    digitalDateEvidence: movieMode === "digital",
  };
  if (next.effort === "gentle" || next.effort === "unlimited") next.effort = "normal";
  return next;
}

/** A blank or non-custom ceiling means Ether picks the daily limit. */
export function customRequestCeiling(cfg: AvailabilityConfig): number | null {
  if (cfg.effort === "custom" && cfg.feedRequests > 0) return nextCeiling(cfg.feedRequests);
  return null;
}

function nextCeiling(n: number): number {
  return n;
}
