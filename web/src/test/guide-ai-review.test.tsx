import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { GuideAIReviewDrawer } from "../pages/livetv/GuideAIReview";
import type { ChannelSummary } from "../pages/livetv/model";

const sources = [
  { id: "playlist", name: "Playlist", updatedAt: "2026-11-05T00:00:00Z" },
  { id: "guide", name: "World guide", updatedAt: "2026-11-05T00:00:00Z" },
];
const channels = (count: number): ChannelSummary[] =>
  Array.from({ length: count }, (_, i) => ({
    id: `c${i}`,
    name: `US: Channel ${String(i).padStart(4, "0")}`,
    number: String(i),
    kind: "iptv",
    enabled: true,
    favorite: false,
    sourceId: "playlist",
    guideId: "",
    group: i % 2 ? "News" : "Sports",
  }));
const proposal = (channel: ChannelSummary) => ({
  channelId: channel.id,
  channelName: channel.name,
  guideId: `g-${channel.id}`,
  guideSourceId: "guide",
  guideSourceName: "World guide",
  guideName: channel.name,
  method: "ai",
  confidence: 0.98,
  reason: "Station and region agree",
  proposalKey: `key-${channel.id}`,
});
beforeEach(() => sessionStorage.clear());
const json = (data: unknown) => new Response(JSON.stringify(data), { status: 200 });

it("keeps a 5000-row review bounded, reviews filtered results with keys, commits once and undoes", async () => {
  const list = channels(5000);
  sessionStorage.setItem(
    "ether-guide-review-v2:all:lineup",
    JSON.stringify({
      rows: Object.fromEntries(
        list.map((channel) => [
          channel.id,
          {
            guideId: "",
            guideSourceId: "",
            checked: true,
            decision: "pending",
            proposal: proposal(channel),
          },
        ]),
      ),
    }),
  );
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      const body = JSON.parse(init.body);
      requests.push(body);
      if (body.ai === "commit")
        return json({ matched: body.selections.length, undoToken: "receipt" });
      if (body.ai === "undo") return json({ matched: 1 });
      return json({ matched: 0, changes: [], ai: { configured: true, estimatedCalls: 0 } });
    }),
  );
  render(
    <GuideAIReviewDrawer
      open
      onClose={() => {}}
      channels={list}
      sources={sources}
      coverage={new Map()}
      onApplied={async () => {}}
    />,
  );
  const grid = await screen.findByRole("grid", { name: "Guide match review" });
  expect(grid).toHaveAttribute("aria-rowcount", "5000");
  expect(within(grid).getAllByRole("row").length).toBeLessThan(20);
  fireEvent.change(screen.getByLabelText("Search channels or guides"), {
    target: { value: "4999" },
  });
  await waitFor(() => expect(grid).toHaveAttribute("aria-rowcount", "1"));
  fireEvent.keyDown(grid, { key: "a" });
  expect(screen.getByRole("button", { name: "Apply 1" })).toBeEnabled();
  fireEvent.keyDown(grid, { key: "r" });
  expect(screen.getByRole("button", { name: "Apply 0" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Accept filtered" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply 1" }));
  await screen.findByRole("button", { name: "Undo guide changes" });
  const commit = requests.find((r) => r.ai === "commit");
  expect(commit).toMatchObject({
    selections: [{ channelId: "c4999", proposalKey: "key-c4999", expectedGuideId: "" }],
  });
  expect(requests.filter((r) => r.ai === "commit")).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Undo guide changes" }));
  await waitFor(() =>
    expect(requests.some((r) => r.ai === "undo" && r.undoToken === "receipt")).toBe(true),
  );
});

it("pauses an in-flight batch and resumes only unfinished channels", async () => {
  const list = channels(16);
  let paid = 0;
  const sent: string[][] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      const body = JSON.parse(init.body);
      if (body.ai === "estimate")
        return json({ changes: [], ai: { configured: true, estimatedCalls: 2 } });
      paid++;
      sent.push(body.channelIds);
      if (paid === 2)
        return new Promise((_resolve, reject) =>
          init.signal.addEventListener(
            "abort",
            () => reject(new DOMException("Aborted", "AbortError")),
            { once: true },
          ),
        );
      return json({
        matched: body.channelIds.length,
        changes: body.channelIds.map((id: string) => proposal(list.find((c) => c.id === id)!)),
      });
    }),
  );
  render(
    <GuideAIReviewDrawer
      open
      onClose={() => {}}
      channels={list}
      sources={sources}
      coverage={new Map()}
      onApplied={async () => {}}
    />,
  );
  const start = await screen.findByRole("button", { name: "Find suggestions" });
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(start);
  await waitFor(() => expect(paid).toBe(2));
  fireEvent.click(screen.getByRole("button", { name: "Pause matching" }));
  const resume = await screen.findByRole("button", { name: "Resume matching" });
  fireEvent.click(resume);
  await waitFor(() => expect(paid).toBe(3));
  expect(sent[0]).toEqual(list.slice(0, 8).map((c) => c.id));
  expect(sent[2]).toEqual(list.slice(8).map((c) => c.id));
  await waitFor(() => expect(screen.getByText("16 of 16 channels checked")).toBeVisible());
});

it("restores the latest undo receipt after reload without treating undone proposals as matched", async () => {
  const list = channels(2);
  list[0]!.guideId = "g-c0";
  list[0]!.guideSourceId = "guide";
  sessionStorage.setItem(
    "ether-guide-review-v2:all:lineup",
    JSON.stringify({
      undoToken: "saved-receipt",
      undoIDs: ["c0"],
      rows: {
        c0: {
          guideId: "",
          guideSourceId: "",
          checked: true,
          applied: true,
          matched: false,
          decision: "accepted",
          proposal: proposal(list[0]!),
        },
      },
    }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      const body = JSON.parse(init.body);
      return json(body.ai === "undo" ? { matched: 1 } : { changes: [], ai: { configured: true } });
    }),
  );
  render(
    <GuideAIReviewDrawer
      open
      onClose={() => {}}
      channels={list}
      sources={sources}
      coverage={new Map([["c0", { channelId: "c0", programmes: 24 }]])}
      onApplied={async () => {}}
    />,
  );
  const undo = await screen.findByRole("button", { name: "Undo guide changes" });
  fireEvent.click(undo);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Accept match for US: Channel 0000" })).toBeEnabled(),
  );
  expect(screen.getByRole("button", { name: "Apply 0" })).toBeDisabled();
});
