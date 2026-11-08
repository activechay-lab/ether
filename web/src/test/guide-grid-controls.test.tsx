import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { GuideGrid } from "../pages/livetv/GuideGrid";
import { ProgrammeInspector } from "../pages/livetv/ProgrammeInspector";
import type { Channel, Programme } from "../pages/livetv/model";

describe("GuideGrid navigation controls", () => {
  const dummyChannel: Channel = {
    id: "ch1",
    number: "1.1",
    name: "News Channel",
    kind: "iptv",
    enabled: true,
    favorite: false,
    group: "News",
    streamUrl: "http://example.com/stream.m3u8",
    epoch: "2026-01-01T00:00:00Z",
    seed: 0,
    shuffle: false,
    items: [],
  };

  it("calls onSetShift with 24h when clicking Tomorrow tab", () => {
    const onSetShift = vi.fn();
    const onShift = vi.fn();
    const now = new Date("2026-11-07T12:00:00Z").getTime();
    const from = new Date("2026-11-07T11:00:00Z");
    const until = new Date("2026-11-08T11:00:00Z");

    render(
      <GuideGrid
        channels={[dummyChannel]}
        guide={new Map()}
        from={from}
        until={until}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={onShift}
        onSetShift={onSetShift}
        onNow={vi.fn()}
        loading={false}
      />,
    );

    const tomorrowTab = screen.getByRole("tab", { name: /Tomorrow/i });
    expect(tomorrowTab).toBeInTheDocument();
    expect(tomorrowTab).toHaveAttribute("aria-selected", "false");

    fireEvent.click(tomorrowTab);
    expect(onSetShift).toHaveBeenCalledWith(24);
  });

  it("correctly highlights active day based on from window", () => {
    const onSetShift = vi.fn();
    const now = new Date("2026-11-07T12:00:00Z").getTime();
    // 24 hours ahead
    const fromTomorrow = new Date("2026-11-08T11:00:00Z");
    const untilTomorrow = new Date("2026-11-09T11:00:00Z");

    render(
      <GuideGrid
        channels={[dummyChannel]}
        guide={new Map()}
        from={fromTomorrow}
        until={untilTomorrow}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={vi.fn()}
        onSetShift={onSetShift}
        onNow={vi.fn()}
        loading={false}
      />,
    );

    const todayTab = screen.getByRole("tab", { name: /Today/i });
    const tomorrowTab = screen.getByRole("tab", { name: /Tomorrow/i });
    expect(todayTab).toHaveAttribute("aria-selected", "false");
    expect(tomorrowTab).toHaveAttribute("aria-selected", "true");
  });

  it("calls onSetShift with absolute target hour for 8 PM jump", () => {
    const onSetShift = vi.fn();
    const now = new Date("2026-11-07T14:00:00").getTime();
    const from = new Date("2026-11-07T13:00:00");
    const until = new Date("2026-11-08T13:00:00");

    render(
      <GuideGrid
        channels={[dummyChannel]}
        guide={new Map()}
        from={from}
        until={until}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={vi.fn()}
        onSetShift={onSetShift}
        onNow={vi.fn()}
        loading={false}
      />,
    );

    const primeBtn = screen.getByRole("button", { name: /8 PM/i });
    fireEvent.click(primeBtn);

    const nowHour = new Date(now).getHours();
    expect(onSetShift).toHaveBeenCalledWith(20 - nowHour);
  });

  it("handles day 2 switch, Later shift, and 8 PM jump calculation accurately", () => {
    const onSetShift = vi.fn();
    const onShift = vi.fn();
    const now = new Date("2026-11-07T12:00:00").getTime();
    // Focused on Day 2 (48h ahead)
    const fromDay2 = new Date("2026-11-09T11:00:00");
    const untilDay2 = new Date("2026-11-10T11:00:00");

    render(
      <GuideGrid
        channels={[dummyChannel]}
        guide={new Map()}
        from={fromDay2}
        until={untilDay2}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={onShift}
        onSetShift={onSetShift}
        onNow={vi.fn()}
        loading={false}
      />,
    );

    // Later button nudges by +3h
    const laterBtn = screen.getByRole("button", { name: /Later/i });
    fireEvent.click(laterBtn);
    expect(onShift).toHaveBeenCalledWith(3);

    // Earlier button nudges by -3h
    const earlierBtn = screen.getByRole("button", { name: /Earlier/i });
    fireEvent.click(earlierBtn);
    expect(onShift).toHaveBeenCalledWith(-3);

    // 8 PM jump on Day 2 targets Day 2's 8 PM (48 + (20 - 12) = 56 hours)
    const primeBtn = screen.getByRole("button", { name: /8 PM/i });
    fireEvent.click(primeBtn);
    expect(onSetShift).toHaveBeenCalledWith(48 + (20 - 12));

    // 11 PM jump on Day 2 targets Day 2's 11 PM (48 + (23 - 12) = 59 hours)
    const lateBtn = screen.getByRole("button", { name: /11 PM/i });
    fireEvent.click(lateBtn);
    expect(onSetShift).toHaveBeenCalledWith(48 + (23 - 12));
  });
});

describe("ProgrammeInspector clipboard copy", () => {
  const dummyChannel: Channel = {
    id: "ch1",
    number: "1.1",
    name: "News Channel",
    kind: "iptv",
    enabled: true,
    favorite: false,
    group: "News",
    streamUrl: "http://example.com/stream.m3u8",
    epoch: "2026-01-01T00:00:00Z",
    seed: 0,
    shuffle: false,
    items: [],
  };

  const dummyProgramme: Programme = {
    id: "p1",
    channelId: "ch1",
    start: "2026-11-07T12:00:00Z",
    end: "2026-11-07T13:00:00Z",
    item: {
      key: 1,
      durationMs: 3600000,
      title: "Evening News",
      summary: "Top stories of the day",
    },
  };

  it("copies stream URL via fallback when navigator.clipboard is unavailable", async () => {
    const originalClipboard = navigator.clipboard;
    // Simulate HTTP environment without navigator.clipboard
    Object.defineProperty(navigator, "clipboard", {
      value: undefined,
      configurable: true,
      writable: true,
    });

    const execCommandMock = vi.fn().mockReturnValue(true);
    document.execCommand = execCommandMock;

    render(
      <ProgrammeInspector
        channel={dummyChannel}
        programme={dummyProgramme}
        now={Date.now()}
        onClose={vi.fn()}
      />,
    );

    const copyBtn = screen.getByRole("button", { name: /Copy link/i });
    expect(copyBtn).toBeInTheDocument();

    fireEvent.click(copyBtn);
    expect(execCommandMock).toHaveBeenCalledWith("copy");

    delete (document as any).execCommand;
    Object.defineProperty(navigator, "clipboard", {
      value: originalClipboard,
      configurable: true,
      writable: true,
    });
  });
});
