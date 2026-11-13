import { fireEvent, screen, waitFor, within, cleanup } from "@testing-library/react";
import { afterEach, it, expect } from "vitest";
import { renderApp } from "./harness";
afterEach(cleanup);
it.each([
  "Trakt",
  "Simkl",
  "Language model",
  "OpenSubtitles",
  "SubDL",
  "TheIntroDB",
  "OMDb and MDBList",
  "PostersPlus",
  "XRDB",
  "BetterPosters",
  "RPDB",
  "Custom posters",
])("%s opens setup before activation", async (name) => {
  renderApp("/services/overview", {}, (f) => {
    f.state.config.posters = undefined;
    f.state.config.trakt.enabled = false;
    f.state.config.simkl = { enabled: false, clientId: "", clientSecret: "" };
    f.state.config.llm.enabled = false;
    f.state.config.openSubtitles.enabled = false;
    f.state.config.subdl.enabled = false;
    f.state.config.markers = { theIntroDb: false, apiKey: "" };
  });
  const toggle =
    name === "TMDB"
      ? await screen.findByRole("button", { name: "Set up TMDB" })
      : await screen.findByRole("switch", { name: name + " enabled" });
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Save and enable" })).toBeInTheDocument();
  expect(
    dialog.querySelectorAll('input,select,button[aria-haspopup="listbox"]').length,
  ).toBeGreaterThan(0);
  const fields = Array.from(dialog.querySelectorAll("label")).map((el) => el.textContent);
  const routes: Record<string, string> = {
    Trakt: "trakt",
    Simkl: "simkl",
    "Language model": "llm",
    OpenSubtitles: "opensubtitles",
    SubDL: "subdl",
    TheIntroDB: "introdb",
    TMDB: "tmdb",
    "OMDb and MDBList": "ratings",
    PostersPlus: "postersplus",
    XRDB: "xrdb",
    BetterPosters: "betterposters",
    RPDB: "rpdb",
    "Custom posters": "custom",
  };
  cleanup();
  renderApp("/services/" + routes[name], {}, (f) => {
    f.state.config.trakt.enabled = true;
    f.state.config.simkl = { enabled: true, clientId: "", clientSecret: "" };
    f.state.config.llm.enabled = true;
    f.state.config.openSubtitles.enabled = true;
    f.state.config.subdl = { enabled: true, apiKey: "" };
    f.state.config.markers = { theIntroDb: true, apiKey: "" };
    f.state.config.posters = {
      version: 1,
      enabled: false,
      source: "tmdb",
      baseUrl: "",
      apiKey: "",
      urlTemplate: "",
      accounts: ["postersplus", "xrdb", "betterposters", "rpdb", "custom"].includes(routes[name]!)
        ? {
            [routes[name]!]: {
              enabled: true,
              source: routes[name]!,
              baseUrl: "",
              apiKey: "",
              urlTemplate: "",
            },
          }
        : {},
    };
  });
  await waitFor(() =>
    expect(
      screen.getByRole("tab", {
        name: name === "OMDb and MDBList" ? "Ratings" : name,
      }),
    ).toHaveAttribute("aria-selected", "true"),
  );
  await waitFor(() => expect(screen.getAllByRole("heading", { name })).toHaveLength(1));
  if (name === "TMDB")
    expect(screen.queryByRole("switch", { name: "TMDB account enabled" })).toBeNull();
  else
    expect(
      await screen.findByRole("switch", { name: name + " account enabled" }),
    ).toBeInTheDocument();
  for (const field of fields)
    if (field) expect((await screen.findAllByText(field)).length).toBeGreaterThan(0);
  if (name === "Language model")
    expect(screen.getByRole("button", { name: "Provider" })).toBeInTheDocument();
});

it("turns TheIntroDB lookups on from its card switch", async () => {
  const { fake } = renderApp("/services/overview", {}, (f) => {
    f.state.config.markers = { theIntroDb: false, apiKey: "" };
  });
  fireEvent.click(await screen.findByRole("switch", { name: "TheIntroDB enabled" }));
  const dialog = await screen.findByRole("dialog");
  fireEvent.change(within(dialog).getByLabelText("Optional TheIntroDB key"), {
    target: { value: "intro-key" },
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save and enable" }));
  await waitFor(() => expect(fake.state.config.markers?.theIntroDb).toBe(true));
  expect(fake.state.config.markers?.apiKey).toBe("intro-key");
});

it("keeps configured TMDB available and opens its settings tab", async () => {
  const { fake } = renderApp("/services/overview", {}, (f) => {
    f.state.config.catalog.tmdbKey = "[configured]";
  });
  const manage = await screen.findByRole("button", { name: "Manage TMDB" });
  expect(screen.queryByRole("switch", { name: "TMDB enabled" })).toBeNull();
  fireEvent.click(manage);
  await screen.findByRole("tab", { name: "TMDB", selected: true });
  expect(screen.getByLabelText("TMDB API key")).toHaveValue("[configured]");
  expect(screen.getByRole("button", { name: "Test TMDB key" })).toBeInTheDocument();
  expect(fake.state.config.catalog.tmdbKey).toBe("[configured]");
});
