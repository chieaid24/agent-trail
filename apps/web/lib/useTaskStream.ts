"use client";

// Live task timeline over SSE (docs/architecture/api.md: Streaming). The
// server replays the whole timeline on first connect and a terminal task
// ends with a "done" event, after which the socket is closed for good.
//
// Reconnection is two-layered: EventSource retries dropped sockets itself
// (echoing Last-Event-ID), and when the browser gives up entirely (a
// non-200 answer mid-outage closes the source permanently) the hook
// re-opens from the last seen cursor via ?last_event_id=. The client never
// gives up; the enclosing page owns hard failures.

import { useEffect, useState } from "react";
import { eventCursor, streamUrl } from "./api";
import type { ActivityEvent, TaskStatus } from "./types";

export type StreamState = "connecting" | "live" | "reconnecting" | "done";

const REOPEN_DELAY_MS = 3000;

export interface TaskStream {
  events: ActivityEvent[];
  state: StreamState;
  // The final task status carried by the "done" event; null while running.
  finalStatus: TaskStatus | null;
}

// after reports whether event a sorts strictly after event b.
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
          return; // never let one malformed frame kill the stream
        }
        lastCursor = eventCursor(event);
        setEvents((prev) => {
          // A reconnect can overlap the tail already received; drop replays.
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
          // done without a status still ends the stream
        }
        setState("done");
        es?.close();
      });

      es.onerror = () => {
        setState("reconnecting");
        // CONNECTING: the browser is already retrying with Last-Event-ID.
        // CLOSED: it gave up; re-open from the cursor ourselves.
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
