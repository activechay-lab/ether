// Temporary until W1's canonical model is merged.
export type PersonalRecipe = {
  ingredients: { kind: string; share: number; enabled: boolean; collections?: number[] }[];
  pauseWhenAway: boolean;
  nameFormat: string;
  repeatHours: number;
  maxShowShare: number;
  nightMaxMinutes: number;
};
export const personalRecipe = (): PersonalRecipe => ({
  ingredients: [
    "continue",
    "nextEpisodes",
    "picked",
    "watchlist",
    "comfort",
    "newReleases",
    "collections",
  ].map((kind, i) => ({ kind, share: [25, 25, 30, 10, 10, 10, 10][i]!, enabled: i < 5 })),
  pauseWhenAway: true,
  nameFormat: "{viewer} TV",
  repeatHours: 24,
  maxShowShare: 30,
  nightMaxMinutes: 60,
});
