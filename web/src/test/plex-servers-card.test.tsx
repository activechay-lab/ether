import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { calls, renderApp } from "./harness";

afterEach(cleanup);

it("keeps Plex inside the shared grid and persists only the global flag", async () => {
  const { fake } = renderApp("/services/overview");
  const card = await screen.findByRole("article", { name: "Plex Media Servers" });
  await waitFor(() => expect(within(card).getByRole("switch")).toBeEnabled());
  const grid = card.closest("ul")!;
  expect(grid).toBeTruthy();
  expect(within(grid).getAllByRole("article")).toHaveLength(3);
  expect(within(card).getByText("On")).toBeInTheDocument();
  fireEvent.click(within(card).getByRole("switch"));
  await waitFor(() => expect(fake.state.config.plexServers?.enabled).toBe(false));
  expect(calls(fake, "PUT", "/config/plexservers")[0]?.body).toEqual({ enabled: false });
  expect(within(card).getByText("Off")).toBeInTheDocument();
  fireEvent.click(within(card).getByRole("switch"));
  await waitFor(() => expect(fake.state.config.plexServers?.enabled).toBe(true));
  expect(within(card).getByRole("link", { name: "Manage Plex Media Servers" })).toHaveAttribute(
    "href",
    "/services/plexservers",
  );
});

it.each([false, true])("shows setup and attention status (problem=%s)", async (problem) => {
  renderApp("/services/overview", {}, (f) => {
    (f.state as unknown as { plexServers: unknown[] }).plexServers = problem
      ? [{ id: 1, name: "PMS", problem: "token", viewers: [] }]
      : [];
  });
  const card = await screen.findByRole("article", { name: "Plex Media Servers" });
  await waitFor(() => expect(within(card).getByRole("switch")).toBeEnabled());
  expect(within(card).getByText(problem ? "Needs attention" : "Not set up")).toBeInTheDocument();
  expect(
    within(card).getByRole("link", {
      name: problem ? "Manage Plex Media Servers" : "Set up Plex Media Servers",
    }),
  ).toBeInTheDocument();
});

it("holds sibling switches during a Plex save and restores state after failure", async () => {
  let release!: () => void;
  const { fake } = renderApp("/services/overview", {}, (f) => {
    const original = f.fetch;
    f.fetch = async (input, init) => {
      if (String(input).endsWith("/config/plexservers") && init?.method === "PUT") {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return new Response(JSON.stringify({ error: "save_failed", message: "Failed" }), {
          status: 500,
        });
      }
      return original(input, init);
    };
  });
  const toggle = await screen.findByRole("switch", { name: "Plex Media Servers enabled" });
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  await waitFor(() => expect(release).toBeTypeOf("function"));
  expect(toggle).toBeDisabled();
  expect(screen.getByRole("switch", { name: "Trakt enabled" })).toBeDisabled();
  expect(screen.getByRole("switch", { name: "Simkl enabled" })).toBeDisabled();
  await act(async () => release());
  await waitFor(() => expect(toggle).toBeEnabled());
  expect(toggle).toBeChecked();
  expect(fake.state.config.plexServers?.enabled).toBe(true);
});
