import { Drawer as AppKitDrawer, Spinner, type DrawerProps, cn } from "@braedonsaunders/appkit-ui";
import {
  Suspense,
  useLayoutEffect,
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { t } from "../i18n";
import { createPortal } from "react-dom";
import { X } from "lucide-react";

const DrawerContext = createContext(false);

/** Associate AppKit's visible record title with the dialog's accessible name. */
function RecordTitle({ children }: { children: ReactNode }) {
  const id = useId();
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const dialog = ref.current?.closest('[role="dialog"]');
    if (dialog) dialog.setAttribute("aria-labelledby", id);
  }, [id]);
  return (
    <span ref={ref} id={id}>
      {children}
    </span>
  );
}

/** Preserve AppKit focus, scroll and Escape handling when editors open a child drawer. */
export function Drawer({
  title,
  stacked,
  bodyClassName,
  children,
  dockLeft,
  ...props
}: DrawerProps & { dockLeft?: number }) {
  const nested = useContext(DrawerContext);
  const Surface = dockLeft === undefined || nested ? AppKitDrawer : DockedDrawer;
  return (
    <DrawerContext.Provider value={true}>
      <Surface
        {...props}
        {...(Surface === DockedDrawer ? { dockLeft } : {})}
        stacked={stacked ?? nested}
        bodyClassName={cn(
          "[&_fieldset]:min-w-0",
          bodyClassName ?? "overflow-y-auto px-4 py-5 sm:px-6",
        )}
        title={<RecordTitle>{title ?? t("Editor")}</RecordTitle>}
      >
        <Suspense
          fallback={
            <div className="grid min-h-32 flex-1 place-items-center p-6">
              <Spinner size={20} label={t("Loading editor")} />
            </div>
          }
        >
          {children}
        </Suspense>
      </Surface>
    </DrawerContext.Provider>
  );
}

const DOCKED_EXIT_MS = 300;

/** A desktop record pane beside a live list. No backdrop or focus trap: owners
 * can click and Tab between the list and editor. Child editors remain modal. */
function DockedDrawer({
  open,
  onClose,
  title,
  description,
  headerActions,
  subtabs,
  bodyClassName,
  panelClassName,
  children,
  footer,
  dockLeft,
}: DrawerProps & { dockLeft?: number }) {
  const panel = useRef<HTMLElement>(null);
  const close = useRef(onClose);
  useLayoutEffect(() => {
    close.current = onClose;
  }, [onClose]);
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement;
    const timer = window.setTimeout(
      () => panel.current?.querySelector<HTMLElement>("button, input, [tabindex='0']")?.focus(),
      0,
    );
    const escape = (event: KeyboardEvent) => {
      if (
        event.key !== "Escape" ||
        document.querySelector('[data-ui-overlay], [data-drawer-layer="nested"]')
      )
        return;
      close.current();
    };
    document.addEventListener("keydown", escape);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("keydown", escape);
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [open]);
  // Stay mounted through the exit so the pane slides out like the AppKit
  // drawers instead of vanishing; reduced motion fades instead of sliding.
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (open) {
      setMounted(true);
      const frame = requestAnimationFrame(() => setShown(true));
      return () => cancelAnimationFrame(frame);
    }
    setShown(false);
    const timer = window.setTimeout(() => setMounted(false), DOCKED_EXIT_MS);
    return () => clearTimeout(timer);
  }, [open]);
  if (!mounted) return null;
  return createPortal(
    <div
      data-drawer-layer="base"
      className="pointer-events-none fixed bottom-0 right-0 top-[var(--drawer-top,0px)] z-50"
      style={{ left: dockLeft }}
    >
      <aside
        ref={panel}
        role="dialog"
        aria-modal="false"
        aria-hidden={!open || undefined}
        tabIndex={-1}
        className={cn(
          open ? "pointer-events-auto" : "pointer-events-none",
          "absolute inset-0 flex flex-col overflow-hidden border-l border-border bg-surface text-fg shadow-lg",
          "transition-[transform,opacity] duration-300 ease-out motion-reduce:transition-opacity motion-reduce:duration-[120ms]",
          shown
            ? "translate-x-0 opacity-100"
            : "translate-x-full opacity-0 motion-reduce:translate-x-0",
          panelClassName,
        )}
      >
        <header className="flex items-center justify-between gap-4 border-b border-border px-6 py-4">
          <div className="min-w-0 space-y-0.5">
            <h2 className="truncate text-base font-semibold text-fg">{title}</h2>
            {description && <p className="text-sm text-fg-muted">{description}</p>}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {headerActions}
            <button
              type="button"
              onClick={onClose}
              aria-label={t("Close")}
              className="rounded-md p-1.5 text-fg-muted transition-colors hover:bg-surface-hover hover:text-fg"
            >
              <X size={18} />
            </button>
          </div>
        </header>
        {subtabs && (
          <div className="shrink-0 border-b border-border bg-surface px-6">{subtabs}</div>
        )}
        <div className={cn("app-scroll min-h-0 flex-1 text-fg", bodyClassName)}>{children}</div>
        {footer && (
          <footer className="flex items-center justify-end gap-2 border-t border-border bg-bg-subtle px-6 py-3">
            {footer}
          </footer>
        )}
      </aside>
    </div>,
    document.body,
  );
}
