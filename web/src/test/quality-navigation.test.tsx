import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { calls, renderApp } from "./harness";
import { RELEASE_SEARCH } from "../pages/settings/schema";
import { destinations } from "../lib/sitemap";
import { settingsIndex } from "../lib/settings-index";

it("gives the checker its own searchable destination and inline controls", async () => {
  renderApp("/quality/check");
  const result = await screen.findByTestId("quality-headline", {}, { timeout: 10000 });
  expect(result).toHaveTextContent("4K");
  expect(screen.getAllByTestId("quality-headline")).toHaveLength(1);
  const viewer = screen.getByRole("button", { name: "Viewer" });
  const row = viewer.closest(".grid")!;
  expect(row).toHaveClass("sm:grid-cols-3");
  expect(within(row as HTMLElement).getByRole("button", { name: "App" })).toBeVisible();
  expect(within(row as HTMLElement).getByRole("radio", { name: "Away" })).toBeVisible();
  expect(destinations().some((d) => d.href === "/quality/check")).toBe(true);
  expect(settingsIndex().find((d) => d.title === "Check quality")?.href).toBe("/quality/check");
});

it("keeps the four search settings visible", () => {
  expect(RELEASE_SEARCH[0]!.more).toBeUndefined();
  expect(RELEASE_SEARCH[0]!.fields.map((f) => f.key)).toEqual([
    "maxCandidates",
    "healthSamples",
    "searchCacheTtl",
    "unavailableCooloff",
  ]);
});

it("orders profile sections before away, source order, and versions", async () => {
  renderApp("/quality/profiles");
  await screen.findByRole("button", { name: "Add profile" }, { timeout: 10000 });
  await screen.findByText("Same-quality versions to offer", {}, { timeout: 10000 });
  const flow = screen.getByRole("navigation", { name: "How quality works" });
  expect(flow.querySelector("[aria-current=step]")).toHaveTextContent("Profiles");
  expect(within(flow).queryByRole("link", { name: /Profiles/ })).not.toBeInTheDocument();
  const headings = screen.getAllByRole("heading").map((h) => h.textContent);
  const order = ["Profiles", "Away from home", "Which source goes first", "Version menu"].map((s) =>
    headings.indexOf(s),
  );
  expect(order.every((i) => i >= 0)).toBe(true);
  expect(order).toEqual([...order].sort((a, b) => a - b));
  expect(screen.queryByTestId("quality-headline")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Import a quality profile" }),
  ).not.toBeInTheDocument();
});

it("switches profiles between movies and shows and badges each card", async () => {
  renderApp("/quality/profiles", {}, (fake) => {
    const profiles = fake.state.config.quality.profiles!;
    profiles.push({ ...structuredClone(profiles[1]!), name: "Shows 1080p", mediaType: "tv" });
  });
  const movies = await screen.findByRole("radio", { name: "Movies" }, { timeout: 10000 });
  const shows = screen.getByRole("radio", { name: "Shows" });
  expect(movies).toHaveAttribute("aria-checked", "true");
  expect(await screen.findByText("4K", {}, { timeout: 10000 })).toBeInTheDocument();
  expect(screen.getByText("Movies & shows")).toBeInTheDocument();

  fireEvent.click(shows);
  expect(await screen.findByText("Shows 1080p", {}, { timeout: 10000 })).toBeInTheDocument();
  expect(screen.queryByText("4K")).not.toBeInTheDocument();
  expect(screen.getAllByText("Shows").length).toBeGreaterThan(1);
});

it("saves the automatic sync cadence as a patch and runs it", async () => {
  const { fake } = renderApp("/quality/profiles");
  await screen.findByRole("switch", { name: "Keep imported profiles in sync" }, { timeout: 10000 });
  fireEvent.click(screen.getByRole("switch", { name: "Keep imported profiles in sync" }));
  fireEvent.click(screen.getByRole("button", { name: "Check for changes" }));
  fireEvent.click(await screen.findByRole("option", { name: "Every 3 days" }, { timeout: 4000 }));
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
  expect(calls(fake, "PUT", "/config/quality")[0]!.body).toEqual({
    sync: { enabled: true, interval: "72h" },
  });
  await waitFor(() => expect(fake.state.config.quality.sync?.interval).toBe("72h"));

  fireEvent.click(screen.getByRole("button", { name: "Sync now" }));
  await waitFor(() => expect(calls(fake, "POST", "/releases/quality/sync")).toHaveLength(1));
  expect(await screen.findByText("2 formats added")).toBeInTheDocument();
});

it("keeps an empty media tab selected so its first profile can be added", async () => {
  renderApp("/quality/profiles", {}, (fake) => {
    fake.state.config.quality.profiles = fake.state.config.quality.profiles!.map((p) => ({
      ...p,
      mediaType: "movie",
    }));
  });
  await screen.findByRole("button", { name: "Add profile" });
  fireEvent.click(screen.getByRole("radio", { name: "Shows" }));
  await waitFor(() =>
    expect(screen.getByRole("radio", { name: "Shows" })).toHaveAttribute("aria-checked", "true"),
  );
  fireEvent.click(screen.getByRole("button", { name: "Add profile" }));
  const drawer = await screen.findByRole("dialog");
  expect(within(drawer).getByRole("radio", { name: "Shows" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
});
