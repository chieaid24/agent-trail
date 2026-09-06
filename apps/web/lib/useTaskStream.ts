"use client";

import { useEffect, useState } from "react";
import { eventCursor, streamUrl } from "./api";
import type { ActivityEvent, TaskStatus } from "./types";

export type StreamState = "connecting" | "live" | "reconnecting" | "done";

const REOPEN_DELAY_MS = 3000;

export interface TaskStream {
  events: ActivityEvent[];
  state: StreamState;
  finalStatus: TaskStatus | null;
}

function after(a: ActivityEvent, b: ActivityEvent): boolean {
  return (
    a.attempt_number > b.attempt_number ||
    (a.attempt_number === b.attempt_number &&
      a.sequence_number > b.sequence_number)
  );
}

export function useTaskStream(taskId: string): TaskStream {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [state, setState] = useState<StreamState>("connecting");
  const [finalStatus, setFinalStatus] = useState<TaskStatus | null>(null);

  useEffect(() => {
    let es: EventSource | null = null;
    let reopenTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    let lastCursor: string | undefined;

    const open = () => {
      if (cancelled) return;
      es = new EventSource(streamUrl(taskId, lastCursor));

      es.onopen = () => setState("live");

      es.onmessage = (m: MessageEvent<string>) => {
        let event: ActivityEvent;
        try {
          event = JSON.parse(m.data) as ActivityEvent;
        } catch {
          return; // bad frame must not kill stream
        }
        lastCursor = eventCursor(event);
        setEvents((prev) => {
          // reconnect can replay received tail; drop dupes
          const last = prev[prev.length - 1];
          if (last && !after(event, last)) return prev;
          return [...prev, event];
        });
      };

      es.addEventListener("done", (m: MessageEvent<string>) => {
        try {
          const data = JSON.parse(m.data) as { status: TaskStatus };
          setFinalStatus(data.status);
        } catch {
          // done without status still ends stream
        }
        setState("done");
        es?.close();
      });

      es.onerror = () => {
        setState("reconnecting");
        // closed = browser gave up retrying; reopen from cursor ourselves
        if (es?.readyState === EventSource.CLOSED) {
          es.close();
          reopenTimer = setTimeout(open, REOPEN_DELAY_MS);
        }
      };
    };

    const initial = setTimeout(() => {
      setEvents([]);
      setState("connecting");
      setFinalStatus(null);
      open();
    }, 0);

    return () => {
      cancelled = true;
      clearTimeout(initial);
      if (reopenTimer !== null) clearTimeout(reopenTimer);
      es?.close();
    };
  }, [taskId]);

  return { events, state, finalStatus };
}
