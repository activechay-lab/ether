import { useEffect, useRef, useState, type ReactNode } from "react";

/** AppKit 0.4 has an exit callback but no enter callback. Its spring clears
 * the panel transform when it settles; observe that completion rather than
 * guessing a timeout (which drifts under CPU throttling and reduced motion).
 * Keep the mounted editor intact throughout exit so its cleanup cannot stall
 * the slide and unsaved state is still owned by LiveTV. */
export function ChannelDrawerBody({ children }: { children: ReactNode }) {
  const root = useRef<HTMLDivElement>(null);
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const panel = root.current?.closest<HTMLElement>('[role="dialog"]');
    if (!panel) return;
    const settled = () => {
      if (!panel.style.transform || panel.style.transform === "none") {
        setReady(true);
        observer.disconnect();
      }
    };
    const observer = new MutationObserver(settled);
    observer.observe(panel, { attributes: true, attributeFilter: ["style"] });
    settled();
    return () => observer.disconnect();
  }, []);
  return (
    <div ref={root} className="min-h-full" aria-busy={!ready}>
      {ready ? (
        children
      ) : (
        <div className="space-y-4 p-6" aria-hidden="true">
          <div className="h-32 rounded-lg bg-bg-subtle" />
          <div className="h-8 rounded-md bg-bg-subtle" />
          <div className="h-64 rounded-lg bg-bg-subtle" />
        </div>
      )}
    </div>
  );
}
