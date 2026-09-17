"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { InsightsState } from "@/components/InsightsPanel";
import { getTaskInsights } from "./api";

export function useTaskInsights(
  taskId: string,
  eventCount: number,
  streamDone: boolean,
  running: boolean,
): { state: InsightsState; retry: () => void } {
  const [result, setResult] = useState<{
    taskId: string;
    state: InsightsState;
  } | null>(null);
  const refreshRef = useRef<(() => Promise<void>) | null>(null);
  const retry = useCallback(() => void refreshRef.current?.(), []);

  useEffect(() => {
    let active = true;
    let inFlight = false;
    let queued = false;
    const refresh = async () => {
      if (inFlight) {
        queued = true;
        return;
      }
      inFlight = true;
      try {
        const insights = await getTaskInsights(taskId);
        if (active) {
          setResult({ taskId, state: { phase: "ready", insights } });
        }
      } catch {
        if (active) {
          setResult({ taskId, state: { phase: "error" } });
        }
      } finally {
        inFlight = false;
        if (active && queued) {
          queued = false;
          void refresh();
        }
      }
    };
    refreshRef.current = refresh;
    void refresh();
    return () => {
      active = false;
      refreshRef.current = null;
    };
  }, [taskId]);

  useEffect(() => {
    if (eventCount === 0) return;
    const refresh = setTimeout(() => void refreshRef.current?.(), 250);
    return () => clearTimeout(refresh);
  }, [taskId, eventCount]);

  useEffect(() => {
    if (!running) return;
    const interval = setInterval(() => void refreshRef.current?.(), 10_000);
    return () => clearInterval(interval);
  }, [taskId, running]);

  useEffect(() => {
    if (!streamDone) return;
    // Span persistence can finish after the final stream event.
    const completionRefreshes = Array.from({ length: 6 }, (_, index) =>
      setTimeout(() => void refreshRef.current?.(), (index + 1) * 1000),
    );
    return () => completionRefreshes.forEach(clearTimeout);
  }, [taskId, streamDone]);

  return {
    state: result?.taskId === taskId ? result.state : { phase: "loading" },
    retry,
  };
}
