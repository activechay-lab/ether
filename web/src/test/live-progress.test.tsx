import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { renderApp } from "./harness";

afterEach(() => localStorage.removeItem("ether.activity.nowView"));

describe.each([
  ["overview", "/", "cards"],
  ["activity cards", "/activity", "cards"],
  ["activity list", "/activity", "list"],
])("Live playback on %s", (_name, path, view) => {
  it.each([undefined, 30000])("shows live status with position %s", async (position) => {
    localStorage.setItem("ether.activity.nowView", view);
    renderApp(path, {}, (fake) => {
      const original = fake.fetch;
      fake.fetch = async (input, init) => {
        const res = await original(input, init);
        const dashboard = String(input).endsWith("/dashboard");
        const sessions = String(input).endsWith("/status/sessions");
        if (!dashboard && !sessions) return res;
        const body = await res.json();
        for (const s of dashboard ? body.sections.sessions : body.value) {
          s.liveChannel = {};
          s.viewOffset = position;
          s.Info.duration = 60e9;
          s.BufferedUntil = 45e9;
        }
        return new Response(JSON.stringify(body), { status: 200 });
      };
    });
    const live = await screen.findByText("Live", { selector: "p" }, { timeout: 4000 });
    const stream = live.closest<HTMLElement>("article, li")!;
    expect(within(stream).queryByText("Waiting for playback position")).not.toBeInTheDocument();
    expect(
      within(stream).queryByText(/% watched|% buffered|\d+:\d+ of \d+:\d+/),
    ).not.toBeInTheDocument();
    expect(within(stream).queryByRole("meter")).not.toBeInTheDocument();
  });
});

it("keeps VOD progress in the activity list", async () => {
  localStorage.setItem("ether.activity.nowView", "list");
  renderApp("/activity", {}, (fake) => {
    const original = fake.fetch;
    fake.fetch = async (input, init) => {
      const res = await original(input, init);
      if (!String(input).endsWith("/status/sessions")) return res;
      const body = await res.json();
      for (const s of body.value) {
        s.viewOffset = 30000;
        s.Info.duration = 60e9;
        s.BufferedUntil = 45e9;
      }
      return new Response(JSON.stringify(body), { status: 200 });
    };
  });
  expect(
    await screen.findByText("50% watched · 75% buffered", {}, { timeout: 4000 }),
  ).toBeInTheDocument();
});
