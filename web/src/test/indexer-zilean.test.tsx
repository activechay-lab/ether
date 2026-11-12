import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Indexer } from "../lib/types";
import { isZilean } from "../pages/sources/Indexers";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

async function openAddZilean() {
  fireEvent.click(await screen.findByRole("button", { name: /Add indexer/ }, WAIT));
  fireEvent.click(await screen.findByRole("menuitem", { name: "Zilean instance…" }));
  return screen.findByRole("dialog", { name: "New Zilean instance" });
}

describe("Sources › Indexers Zilean instances", () => {
  it("adds a self-hosted Zilean instance as a torrent source", async () => {
    const { fake } = renderApp("/sources/indexers");
    const drawer = await openAddZilean();
    expect(within(drawer).getByText("Zilean · Torrent")).toBeInTheDocument();
    expect(within(drawer).queryByLabelText("API key")).toBeNull();
    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "My Zilean" } });
    fireEvent.change(within(drawer).getByLabelText("Instance URL"), {
      target: { value: "  http://zilean:8181  " },
    });

    fireEvent.click(within(drawer).getByRole("button", { name: /Test/ }));
    await waitFor(() => expect(calls(fake, "POST", "/test/indexer")).toHaveLength(1));
    expect(calls(fake, "POST", "/test/indexer")[0]!.body).toMatchObject({
      kind: "zilean",
      url: "http://zilean:8181",
    });

    fireEvent.click(within(drawer).getByRole("button", { name: "Save indexer" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/indexers")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/indexers")[0]!.body as Indexer[];
    expect(body.at(-1)).toMatchObject({
      name: "My Zilean",
      kind: "zilean",
      protocol: "torrent",
      url: "http://zilean:8181",
      enabled: true,
    });
    const list = screen.getByRole("region", { name: "Indexers" });
    await waitFor(() => expect(within(list).getAllByText("Zilean").length).toBeGreaterThan(0));
  });

  it("recognizes a Zilean kind", () => {
    expect(isZilean({ kind: "zilean" })).toBe(true);
    expect(isZilean({ kind: "stremio" })).toBe(false);
  });
});
