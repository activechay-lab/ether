import type { PersonalRecipe, IngredientKind } from "./model";
export const personalRecipe = (): PersonalRecipe => ({
  ingredients: [
    "continue",
    "nextEpisodes",
    "picked",
    "watchlist",
    "comfort",
    "newReleases",
    "collections",
  ].map((kind, i) => ({
    kind: kind as IngredientKind,
    share: [25, 25, 30, 10, 10, 10, 10][i]!,
    enabled: i < 5,
  })),
  pauseWhenAway: true,
  nameFormat: "{viewer} TV",
  repeatHours: 24,
  maxShowShare: 30,
  nightMaxMinutes: 60,
});
