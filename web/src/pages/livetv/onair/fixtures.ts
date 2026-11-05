import type { Look, Family } from "./model";
export const referenceLooks: Look[] = (
  [
    [
      "nocturne",
      "Nocturne",
      "lateNight",
      "#060508",
      "#C8102E",
      "#EDE6D6",
      "#8FA3B8",
      "Playfair Display",
      "Special Elite",
      "Late night · lights off",
    ],
    [
      "meridian",
      "Meridian",
      "cinema",
      "#070d1d",
      "#D8B26E",
      "#F4EEDF",
      "#17254a",
      "Fraunces",
      "Inter",
      "A world of cinema",
    ],
    [
      "blorp",
      "BLORP!",
      "kids",
      "#6B2BD9",
      "#FF7A00",
      "#ffffff",
      "#7CE03A",
      "Bungee",
      "Fredoka",
      "Totally gooey TV",
    ],
    [
      "neon",
      "NEON 84",
      "retro",
      "#12002b",
      "#ff4fd8",
      "#ffffff",
      "#22e6ff",
      "Orbitron",
      "VT323",
      "Stay tuned",
    ],
    [
      "personal",
      "Your TV",
      "personal",
      "#100b23",
      "#9b85ff",
      "#ffffff",
      "#ff5ca8",
      "Space Grotesk",
      "Inter",
      "Made for you",
    ],
  ] as [string, string, string, string, string, string, string, string, string, string][]
).map(([id, name, family, background, accent, text, glow, display, body, tagline], i) => ({
  id,
  name,
  family,
  brand: {
    palette: { background, accent, text, glow },
    type: { display, body },
    energy: 0.5,
    textures: ["Grain"],
    sting: "Soft chime",
    volume: 0.6,
    seed: 0,
    tagline,
    language: "en",
    words: {},
  },
  moments: Object.fromEntries(
    ["ident", "tonight", "upnext", "break", "back", "offair", "welcome", "bug", "lowerthird"].map(
      (m) => [
        m,
        {
          duration: 8,
          poster:
            m === "ident"
              ? family === "retro"
                ? 5.4
                : family === "lateNight"
                  ? 5.3
                  : family === "kids"
                    ? 3.8
                    : 4.4
              : family === "lateNight" && m === "back"
                ? 6.2
                : 4.4,
        },
      ],
    ),
  ),
  channelIds:
    i === 0
      ? ["v-movie-night", "v-horror"]
      : i === 4
        ? ["v-personal"]
        : i === 2
          ? ["v-family"]
          : i === 3
            ? ["v-80s"]
            : ["v-movie-night"],
  ...(i === 2 ? { pack: { id: "saturday", name: "Saturday mornings" } } : {}),
}));
export const families: Family[] = ["clean", "kids", "lateNight", "cinema", "retro", "personal"].map(
  (id, i) => ({
    id,
    name: ["Clean", "Kids", "Late night", "Cinema", "Retro", "Personal"][i]!,
    look: {
      ...(referenceLooks.find((l) => l.family === id) || referenceLooks[1]!),
      id: "",
      family: id,
      ...(id === "clean"
        ? {
            name: "Clean",
            channelIds: [],
            brand: {
              ...referenceLooks[1]!.brand,
              palette: {
                background: "#0b1320",
                accent: "#78b9e8",
                text: "#f2f5fa",
                glow: "#17254a",
              },
              type: { display: "Inter", body: "Inter" },
              tagline: "A little more to discover",
            },
          }
        : {}),
    },
  }),
);
