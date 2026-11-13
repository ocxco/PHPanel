import { useEffect, useRef, useState } from "react";
import { call, desktop, emptyState, type State, type StateUpdate } from "./api";

export function usePanelState() {
  const [state, setState] = useState<State>(emptyState);
  const [connectionError, setConnectionError] = useState("");
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  useEffect(() => {
    let disposed = false,
      revision = 0,
      pending = false;
    let inFlight: Promise<void> | null = null;
    let scheduled: ReturnType<typeof setTimeout> | undefined;
    function schedule() {
      if (disposed || document.hidden || scheduled !== undefined) return;
      scheduled = setTimeout(() => {
        scheduled = undefined;
        void refresh();
      }, 200);
    }
    function refresh(): Promise<void> {
      if (disposed || !desktop() || document.hidden) return Promise.resolve();
      if (inFlight) {
        pending = true;
        return inFlight;
      }
      inFlight = (async () => {
        try {
          const update = await call<StateUpdate>("SnapshotSince", revision);
          if (disposed) return;
          if (update.changed && update.state) {
            revision = update.revision;
            setState(update.state);
          }
          setConnectionError("");
        } catch (e) {
          if (!disposed) setConnectionError(String(e));
        }
      })().finally(() => {
        inFlight = null;
        if (pending && !disposed) {
          pending = false;
          schedule();
        }
      });
      return inFlight;
    }
    refreshRef.current = refresh;
    // Subscribe before the first snapshot so startup changes cannot be missed.
    const unsubscribe = window.runtime?.EventsOnMultiple?.(
      "panel:changed",
      schedule,
      -1,
    );
    const onVisible = () => {
      if (!document.hidden) void refresh();
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onVisible);
    // A revision-only response reconciles missed events without transferring or
    // rerendering unchanged catalogues. Hidden windows skip this check entirely.
    const reconcile = setInterval(
      () => void refresh(),
      unsubscribe ? 30000 : 5000,
    );
    void refresh();
    return () => {
      disposed = true;
      clearInterval(reconcile);
      if (scheduled !== undefined) clearTimeout(scheduled);
      unsubscribe?.();
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("focus", onVisible);
    };
  }, []);
  return { state, connectionError, refresh: () => refreshRef.current() };
}
