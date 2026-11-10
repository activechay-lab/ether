import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "./harness";
import { calls, settleUi } from "./harness";

describe("Libraries workspace", () => {
  it("lists libraries and redirects the old catalogue route", async () => {
    renderApp("/libraries");
    expect(
      await screen.findByRole("heading", { name: "Libraries", level: 1 }, { timeout: 15000 }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Books")).toBeInTheDocument();
    expect(screen.getAllByText("Movies").length).toBeGreaterThan(0);
  });

  it("creates a console-only filter library and opens it", async () => {
    const { fake } = renderApp("/libraries");
    fireEvent.click(await screen.findByRole("button", { name: "New library" }, { timeout: 15000 }));
    fireEvent.change(await screen.findByLabelText("Library name"), {
      target: { value: "Weekend films" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create library" }));
    await settleUi(() => {
      const post = calls(fake, "POST", "/libraries").at(-1);
      expect(post?.body).toMatchObject({ name: "Weekend films" });
      expect((post?.body as { present?: unknown }).present).toBeUndefined();
    });
    expect(
      await screen.findByRole("heading", { name: "Weekend films", level: 1 }, { timeout: 15000 }),
    ).toBeInTheDocument();
  });

  it("browses a plugin library and re-matches from the item drawer", async () => {
    const { fake } = renderApp("/libraries/3");
    fireEvent.click(await screen.findByRole("button", { name: /Dune/ }, { timeout: 15000 }));
    expect(await screen.findByText("Matching")).toBeInTheDocument();
    expect(screen.getByText("Herbert")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Match again" }));
    await settleUi(() => {
      expect(calls(fake, "POST", "/libraries/3/item/").length).toBeGreaterThan(0);
    });
  });

  it("shows plugin-only sections in a plugin library's settings", async () => {
    renderApp("/libraries/3/settings");
    expect(
      await screen.findByText("Keeping it up to date", {}, { timeout: 15000 }),
    ).toBeInTheDocument();
    expect(screen.getByText("Downloaded files")).toBeInTheDocument();
    expect(screen.queryByText("New releases")).not.toBeInTheDocument();
  });
});
