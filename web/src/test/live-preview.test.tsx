import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { LivePreview } from "../pages/livetv/LivePreview";
import type { Channel, PreviewSession } from "../pages/livetv/model";
const mocks = vi.hoisted(() => ({
  play: vi.fn(),
  stop: vi.fn(),
  destroy: vi.fn(),
  config: vi.fn(),
}));
vi.mock("../pages/livetv/model", () => ({
  hueOf: () => 120,
  tv: { play: mocks.play, stopPreview: mocks.stop },
}));
vi.mock("hls.js", () => ({
  default: class {
    constructor(config: unknown) {
      mocks.config(config);
    }
    static isSupported() {
      return true;
    }
    static Events = { ERROR: "error", LEVEL_SWITCHED: "level" };
    loadSource() {}
    attachMedia() {}
    on() {}
    destroy = mocks.destroy;
  },
}));
const channel = (id: string) => ({ id, name: id, number: "1" }) as Channel;
const session = (id: string) => ({ id, playlist: `sessions/${id}/index.m3u8` }) as PreviewSession;
beforeEach(() => {
  vi.clearAllMocks();
  mocks.stop.mockResolvedValue({});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
});
it("stops a late tune before tuning the next channel and viewer", async () => {
  let finish!: (s: PreviewSession) => void;
  mocks.play.mockReturnValueOnce(
    new Promise<PreviewSession>((resolve) => {
      finish = resolve;
    }),
  );
  mocks.play.mockResolvedValueOnce(session("b"));
  let stopped!: () => void;
  mocks.stop.mockReturnValueOnce(
    new Promise<void>((resolve) => {
      stopped = resolve;
    }),
  );
  const view = render(<LivePreview channel={channel("a")} onClose={() => {}} />);
  await waitFor(() => expect(mocks.play).toHaveBeenCalledWith("a", undefined));
  view.rerender(<LivePreview channel={channel("b")} viewer="2" onClose={() => {}} />);
  await act(async () => {
    finish(session("a"));
  });
  await waitFor(() => expect(mocks.stop).toHaveBeenCalledWith("a"));
  expect(mocks.play).toHaveBeenCalledTimes(1);
  await act(async () => {
    stopped();
  });
  await waitFor(() => expect(mocks.play).toHaveBeenCalledWith("b", "2"));
  await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
  view.unmount();
  await waitFor(() => expect(mocks.stop).toHaveBeenCalledWith("b"));
  expect(mocks.destroy).toHaveBeenCalledTimes(1);
});

it("bounds cold manifest retries and refuses permanent HTTP errors", async () => {
  mocks.play.mockResolvedValue(session("a"));
  const view = render(<LivePreview channel={channel("a")} onClose={() => {}} />);
  await waitFor(() => expect(mocks.config).toHaveBeenCalled());
  const config = mocks.config.mock.calls[0]![0];
  const retry = config.manifestLoadPolicy.default.errorRetry;
  expect(retry.shouldRetry(retry, 0, false, { code: 503 })).toBe(true);
  expect(retry.shouldRetry(retry, 0, false, { code: 502 })).toBe(true);
  expect(retry.shouldRetry(retry, 0, true)).toBe(true);
  expect(retry.shouldRetry(retry, 0, false, { code: 403 })).toBe(false);
  expect(retry.shouldRetry(retry, 8, false, { code: 503 })).toBe(false);
  const clock = vi.spyOn(performance, "now").mockReturnValue(Infinity);
  expect(retry.shouldRetry(retry, 1, false, { code: 503 })).toBe(false);
  clock.mockRestore();
  view.unmount();
});
