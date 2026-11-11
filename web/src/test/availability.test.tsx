import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { applyReleaseMode, releaseMode } from "../lib/release-mode";
import { destinationsFrom } from "../lib/sitemap";
import { descriptors as consoleDescriptors } from "../mock/extend/descriptors/console";
import type { PageMeta } from "../lib/console-descriptors";
import type { Descriptor } from "../lib/extend";

const destinations = () =>
  destinationsFrom(consoleDescriptors.filter((d) => d.kind === "page") as Descriptor<PageMeta>[]);
import { calls, renderApp, settleUi } from "./harness";

const matchMedia = window.matchMedia;

beforeEach(() => {
  window.matchMedia = (query: string) => ({
    matches: query.includes("min-width") || query.includes("prefers-reduced-motion"),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  });
});
afterEach(() => {
  window.matchMedia = matchMedia;
});

describe("New release modes", () => {
  it("maps the shipped default to When they can play", () => {
    expect(
      releaseMode({
        monitor: true,
        verify: false,
        preferVerified: true,
        newlyAvailable: true,
        indexerIds: [],
        debrid: true,
        pollMinutes: 15,
        maxPages: 5,
        feedRequests: 400,
        checkRequests: 100,
        grabs: 20,
        checks: 20,
        checkBytes: 1,
        reservePercent: 80,
      }),
    ).toBe("playable");
    expect(
      releaseMode({
        disableMovieEvidenceGate: true,
        monitor: false,
        verify: false,
        preferVerified: true,
        newlyAvailable: true,
        indexerIds: [],
        debrid: true,
        pollMinutes: 15,
        maxPages: 5,
        feedRequests: 400,
        checkRequests: 100,
        grabs: 20,
        checks: 20,
        checkBytes: 1,
        reservePercent: 80,
      }),
    ).toBe("immediate");
    const digital = applyReleaseMode(
      {
        monitor: true,
        verify: false,
        preferVerified: true,
        newlyAvailable: true,
        indexerIds: [],
        debrid: true,
        pollMinutes: 15,
        maxPages: 5,
        feedRequests: 400,
        checkRequests: 100,
        grabs: 20,
        checks: 20,
        checkBytes: 1,
        reservePercent: 80,
        effort: "gentle",
      },
      "digital",
    );
    expect(digital).toMatchObject({
      mode: "digital",
      monitor: false,
      digitalDateEvidence: true,
      disableMovieEvidenceGate: false,
      effort: "normal",
    });
  });

  it("lives in per-library Settings, not Sources or a global page", async () => {
    renderApp("/libraries/1/settings");
    await settleUi(() => {
      expect(screen.getByRole("radio", { name: /When they can play/ })).toBeVisible();
    });
    expect(screen.getByText("Recommended")).toBeVisible();
    expect(
      screen.getByText(
        "A new movie appears once Ether finds a release you can play with your quality settings. Ether checks for you automatically.",
      ),
    ).toBeVisible();
    expect(screen.getByRole("link", { name: /waiting for a release/ })).toHaveAttribute(
      "href",
      "/libraries/1?waiting=1",
    );
    expect(
      screen.queryByRole("switch", { name: "Look for playable releases" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Gentle")).not.toBeInTheDocument();
    expect(screen.queryByText("Indexer requests today")).not.toBeInTheDocument();
    expect(destinations().some((d) => d.href === "/sources/availability")).toBe(false);
    expect(
      destinations()
        .filter((d) => d.href.startsWith("/libraries/"))
        .map((d) => d.href),
    ).toEqual([
      "/libraries/1/rules",
      "/libraries/1/settings",
      "/libraries/2/rules",
      "/libraries/2/settings",
    ]);
  });

  it("saves On digital release for Movies without turning off TV monitoring", async () => {
    const { fake } = renderApp("/libraries/1/settings");
    await settleUi(() => {
      expect(screen.getByRole("radio", { name: /On digital release/ })).toBeVisible();
    });
    expect(screen.queryByText("Ready Movie")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: /On digital release/ }));
    expect(calls(fake, "PUT", "/config/availability")).toHaveLength(0);
    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    fireEvent.click(within(bar).getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PUT", "/config/availability")).toHaveLength(1);
    });
    expect(calls(fake, "PUT", "/config/availability")[0]!.body).toMatchObject({
      mode: "digital",
      movieMode: "digital",
      showMode: "playable",
      monitor: true,
      digitalDateEvidence: true,
      disableMovieEvidenceGate: false,
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("edits TV Shows release mode independently with episode copy", async () => {
    const { fake } = renderApp("/libraries/2/settings");
    await settleUi(() => {
      expect(screen.getByRole("radio", { name: /When they can play/ })).toBeVisible();
    });
    expect(screen.queryByRole("radio", { name: /On digital release/ })).not.toBeInTheDocument();
    expect(
      screen.getByText(
        "A new episode or show appears once Ether finds a release you can play with your quality settings. Ether checks for you automatically.",
      ),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("radio", { name: /Right away/ }));
    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    fireEvent.click(within(bar).getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PUT", "/config/availability")).toHaveLength(1);
    });
    expect(calls(fake, "PUT", "/config/availability")[0]!.body).toMatchObject({
      movieMode: "playable",
      showMode: "immediate",
      monitor: true,
      disableMovieEvidenceGate: false,
    });
  });

  it("keeps the daily budget on Sources › Indexers, behind More options", async () => {
    const { fake } = renderApp("/sources/indexers");
    const more = await screen.findByRole("button", { name: "More options: Daily budgets" });
    fireEvent.click(more);
    const field = screen.getByLabelText("Daily budget for new-release checks");
    fireEvent.change(field, { target: { value: "48" } });
    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    fireEvent.click(within(bar).getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PUT", "/config/availability")).toHaveLength(1);
    });
    expect(calls(fake, "PUT", "/config/availability")[0]!.body).toMatchObject({
      effort: "custom",
      feedRequests: 48,
    });
  });

  it("merges the release-date gate into the three release cards", async () => {
    renderApp("/libraries/1/settings");
    expect(await screen.findByRole("radio", { name: /On digital release/ })).toBeVisible();
    fireEvent.click(screen.getByRole("radio", { name: /Right away/ }));
    expect(
      screen.queryByRole("switch", { name: "Wait for digital or physical release" }),
    ).not.toBeInTheDocument();
  });

  it("edits and saves Home-release offset (days) alongside On digital release", async () => {
    const { fake } = renderApp("/libraries/1/settings");
    await settleUi(() => {
      expect(screen.getByRole("radio", { name: /On digital release/ })).toBeVisible();
    });
    expect(screen.queryByLabelText("Home-release offset (days)")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: /On digital release/ }));
    const offset = screen.getByLabelText("Home-release offset (days)");
    expect(offset).toHaveValue(0);

    fireEvent.change(offset, { target: { value: "-21" } });
    expect(offset).toHaveValue(-21);

    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    fireEvent.click(within(bar).getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PUT", "/config/availability")).toHaveLength(1);
    });
    expect(calls(fake, "PUT", "/config/availability")[0]!.body).toMatchObject({
      mode: "digital",
      movieMode: "digital",
      digitalDateEvidence: true,
      homeReleaseOffsetDays: -21,
    });
  });
});
