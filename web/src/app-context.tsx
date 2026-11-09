import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api } from "./lib/api";
import { computeHealth, overallLevel, type HealthItem, type HealthLevel } from "./lib/health";
import type { Bootstrap, DashboardData } from "./lib/types";
import { useLocale } from "./i18n/react";
import { useStreamEvent } from "./lib/stream";
import { invalidateRegistry } from "./lib/extend";

interface AppState {
  boot: Bootstrap;
  reloadBoot: () => Promise<void>;
  dashboard: DashboardData | null;
  dashboardError: unknown;
  refreshDashboard: () => Promise<void>;
  /** Poll faster while a screen shows live data (now playing). */
  setLivePolling: (on: boolean) => void;
  health: HealthItem[];
  overall: HealthLevel;
}

const Ctx = createContext<AppState | null>(null);

/** Live screens refresh at this cadence; everything else trusts the stream. */
const LIVE_MS = 30_000;
/** Safety net when the stream drops events or never connects. */
const SAFETY_MS = 300_000;

export function AppProvider({
  boot,
  reloadBoot,
  children,
}: {
  boot: Bootstrap;
  reloadBoot: () => Promise<void>;
  children: ReactNode;
}) {
  const [dashboard, setDashboard] = useState<DashboardData | null>(null);
  const [dashboardError, setDashboardError] = useState<unknown>(null);
  const [live, setLive] = useState(0);
  const inflight = useRef(false);

  const refreshDashboard = useCallback(async () => {
    if (inflight.current) return;
    inflight.current = true;
    try {
      setDashboard(await api.dashboard());
      setDashboardError(null);
    } catch (e) {
      setDashboardError(e);
    } finally {
      inflight.current = false;
    }
  }, []);

  useEffect(() => {
    void refreshDashboard();
  }, [refreshDashboard]);
  // Push updates arrive over the console stream; polling is only the safety
  // net. A pushed payload replaces state directly, so a change lands within
  // a second instead of waiting for the next tick.
  useStreamEvent<DashboardData>("dashboard", (event) => {
    const data = event.data;
    if (data && typeof data === "object" && "sections" in data && "names" in data) {
      inflight.current = false;
      setDashboard(data);
      setDashboardError(null);
      if (
        (typeof data.setupComplete === "boolean" && data.setupComplete !== boot.setupComplete) ||
        (typeof data.serverName === "string" && data.serverName !== boot.serverName)
      ) {
        void reloadBoot();
      }
    }
  });
  useStreamEvent("extend.changed", () => invalidateRegistry());

  useEffect(() => {
    const ms = live > 0 ? LIVE_MS : SAFETY_MS;
    const t = window.setInterval(() => {
      if (document.visibilityState !== "hidden") void refreshDashboard();
    }, ms);
    return () => window.clearInterval(t);
  }, [live, refreshDashboard]);

  const setLivePolling = useCallback(
    (on: boolean) => setLive((n) => Math.max(0, n + (on ? 1 : -1))),
    [],
  );

  const { locale } = useLocale();
  const health = useMemo(
    () => computeHealth(dashboard?.sections, new Date(), { plexEnabled: dashboard?.plexEnabled }),
    [dashboard, locale],
  );
  const overall = useMemo<HealthLevel>(() => {
    if (dashboardError) return "error";
    if (!dashboard) return "unknown";
    return overallLevel(health);
  }, [health, dashboard, dashboardError]);

  const value = useMemo<AppState>(
    () => ({
      boot,
      reloadBoot,
      dashboard,
      dashboardError,
      refreshDashboard,
      setLivePolling,
      health,
      overall,
    }),
    [
      boot,
      reloadBoot,
      dashboard,
      dashboardError,
      refreshDashboard,
      setLivePolling,
      health,
      overall,
    ],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useApp(): AppState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useApp must be used inside AppProvider");
  return v;
}

/** The app state when rendered inside the shell, null otherwise (standalone screens, tests). */
export function useAppOptional(): AppState | null {
  return useContext(Ctx);
}

/** Keep fast polling on while the calling screen is mounted. */
export function useLiveDashboard(): void {
  const { setLivePolling } = useApp();
  useEffect(() => {
    setLivePolling(true);
    return () => setLivePolling(false);
  }, [setLivePolling]);
}
