import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

describe("client quality rules", () => {
  it("scopes a rule to shows and filters the list by media", async () => {
    const { fake } = renderApp("/quality/rules", {}, (f) => {
      const profiles = f.state.config.quality.profiles!;
      profiles.push({ ...structuredClone(profiles[1]!), name: "Shows 1080p", mediaType: "tv" });
      f.state.config.quality.rules![0]!.mediaType = "movie";
    });
    const firstRule = (await screen.findByText(/1\. Plex for Apple TV/, {}, WAIT)).closest("li")!;
    expect(within(firstRule).getByText("Movies")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    const drawer = await screen.findByRole("dialog", { name: "New rule" }, WAIT);
    const applies = within(drawer).getByRole("radiogroup", { name: "Applies to" });
    fireEvent.click(within(applies).getByRole("radio", { name: "Shows" }));

    // The profile picker offers only profiles that serve shows.
    fireEvent.click(within(drawer).getByRole("button", { name: "Profile" }));
    fireEvent.click(await screen.findByRole("option", { name: "Shows 1080p" }, WAIT));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);

    const saved = fake.state.config.quality.rules!.at(-1)!;
    expect(saved).toMatchObject({ mediaType: "tv", profile: "Shows 1080p" });

    const filter = screen.getByRole("radiogroup", { name: "Show rules for" });
    fireEvent.click(within(filter).getByRole("radio", { name: "Shows" }));
    expect(screen.getAllByRole("button", { name: /Edit rule/ })).toHaveLength(1);
    expect(screen.queryByText(/1\. Plex for Apple TV/)).not.toBeInTheDocument();

    fireEvent.click(within(filter).getByRole("radio", { name: "Movies" }));
    expect(screen.getAllByRole("button", { name: /Edit rule/ })).toHaveLength(1);
    expect(screen.getByText(/1\. Plex for Apple TV/)).toBeInTheDocument();

    expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1);
  });
});
