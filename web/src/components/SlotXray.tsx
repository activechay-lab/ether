import type { ReactNode } from "react";

/** Layout wrapper for a live plugin extension slot. */
export function SlotAnchor({ children, className }: { children?: ReactNode; className?: string }) {
  return <div className={className}>{children}</div>;
}
