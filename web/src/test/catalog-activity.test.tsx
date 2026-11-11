import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderAppWith } from "./routes";

describe("Catalog recent activity", () => {
  it("reports real cross-media sync counters", async () => {
    renderAppWith("/libraries/1/settings", (_method, path) => {
      if (path === "/status/catalog")
        return {
          value: { lastSync: "2026-11-02T12:00:00Z", sync: { titlesAdded: 42, titlesSeen: 1234 } },
        };
    });
    const activity = within(await screen.findByRole("region", { name: "Recent activity" }));
    expect(activity.getByText("Added in last sync")).toBeInTheDocument();
    expect(activity.getByText("42")).toBeInTheDocument();
    expect(activity.getByText("Checked in last sync")).toBeInTheDocument();
    expect(activity.getByText("1,234")).toBeInTheDocument();
    expect(activity.queryByText(/^Synced /)).not.toBeInTheDocument();
  });

  it("does not fabricate activity before the first sync", async () => {
    renderAppWith("/libraries/1/settings", (_method, path) => {
      if (path === "/status/catalog")
        return {
          value: { lastSync: "0001-01-01T00:00:00Z", sync: { titlesAdded: 0, titlesSeen: 0 } },
        };
    });
    expect(await screen.findByText("No sync activity yet")).toBeInTheDocument();
    expect(screen.queryByText("Added in last sync")).not.toBeInTheDocument();
  });
});
