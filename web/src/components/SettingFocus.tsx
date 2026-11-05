import { useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";

const PREFIX = "#setting=";

/**
 * A link that opens a page and points at one setting on it. `key` is the
 * row's `data-setting`; `more` is the `data-more` of the "More options"
 * disclosure that hides it. Both are stable ids, never display text.
 */
export function settingHref(href: string, key: string, more?: string): string {
  const q = new URLSearchParams({ s: key });
  if (more) q.set("m", more);
  return `${href}${PREFIX}${q.toString()}`;
}

/** Marks one setting row so a ⌘K jump can find it by key. */
export function SettingAnchor({ id, children }: { id: string; children: ReactNode }) {
  return <div data-setting={id}>{children}</div>;
}

function byData<T extends HTMLElement>(
  root: ParentNode,
  selector: string,
  attr: "setting" | "more",
  value: string,
): T | null {
  return (
    Array.from(root.querySelectorAll<T>(selector)).find((el) => el.dataset[attr] === value) ?? null
  );
}

/**
 * Follows a ⌘K jump to one setting: waits for the page to render, opens the
 * "More options" disclosure that hides it, then scrolls to and highlights it.
 */
export function SettingFocus() {
  const location = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (!location.hash.startsWith(PREFIX)) return;
    const q = new URLSearchParams(location.hash.slice(PREFIX.length));
    const key = q.get("s") ?? "";
    const more = q.get("m");
    let tries = 0;
    let opened = false;
    const timer = window.setInterval(() => {
      tries++;
      const root = document.querySelector("main") ?? document.body;
      const anchor = key ? byData<HTMLElement>(root, "[data-setting]", "setting", key) : null;
      const row = anchor?.closest<HTMLElement>(".px-5") ?? anchor;
      if (row) {
        window.clearInterval(timer);
        row.scrollIntoView({ block: "center", behavior: "smooth" });
        row.classList.add("setting-focus");
        window.setTimeout(() => row.classList.remove("setting-focus"), 2400);
        navigate(`${location.pathname}${location.search}`, { replace: true });
        return;
      }
      if (more && !opened) {
        const button = byData<HTMLButtonElement>(
          root,
          'button[aria-expanded="false"][data-more]',
          "more",
          more,
        );
        if (button) {
          button.click();
          opened = true;
        }
      }
      if (tries > 40) {
        window.clearInterval(timer);
        navigate(`${location.pathname}${location.search}`, { replace: true });
      }
    }, 100);
    return () => window.clearInterval(timer);
  }, [location.hash, location.pathname, location.search, navigate]);
  return null;
}
