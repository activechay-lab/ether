import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// Page tests render lazy routes against the mock backend. On a loaded
// machine (parallel builds, CI runners) that can take longer than Testing
// Library's 1 s default, so findBy/waitFor wait up to 5 s. Real failures
// still fail; slow-but-correct renders no longer flake.
configure({ asyncUtilTimeout: 5000 });

// Node 25+ defines its own (file-backed, here unconfigured) Web Storage
// globals, which stop vitest from installing jsdom's. Use jsdom's.
const dom = (globalThis as { jsdom?: { window: Window } }).jsdom;
if (dom) {
  for (const key of ["localStorage", "sessionStorage"] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      writable: true,
      value: dom.window[key],
    });
  }
}

// jsdom lacks a few browser APIs that AppKit and the app use.
if (!window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: query.includes("min-width"), // render the desktop layout in tests
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

class NoopObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}
if (!("ResizeObserver" in window))
  Object.defineProperty(window, "ResizeObserver", {
    configurable: true,
    writable: true,
    value: NoopObserver,
  });
if (!("IntersectionObserver" in window))
  Object.defineProperty(window, "IntersectionObserver", {
    configurable: true,
    writable: true,
    value: NoopObserver,
  });
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
if (!Element.prototype.scrollBy) Element.prototype.scrollBy = () => {};
if (!window.scrollTo) window.scrollTo = () => {};

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
});
