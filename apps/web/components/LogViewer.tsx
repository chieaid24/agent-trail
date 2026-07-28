"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { deriveLogLines, filterLogLines, type LogLine } from "@/lib/logs";
import type { ActivityEvent } from "@/lib/types";

// Virtualized terminal-style log viewer (DESIGN.md: mono 13px, follow mode
// pinned to bottom, visible redaction markers). Windowing is hand-rolled:
// fixed row height, spacer above and below the rendered slice.
const ROW_HEIGHT = 20;
const OVERSCAN = 20;
const PANE_HEIGHT = 480;

export function LogViewer({ events }: { events: ActivityEvent[] }) {
  const allLines = useMemo(() => deriveLogLines(events), [events]);
  const [query, setQuery] = useState("");
  const lines = useMemo(
    () => filterLogLines(allLines, query),
    [allLines, query],
  );

  const paneRef = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  const [scrollTop, setScrollTop] = useState(0);

  // Follow mode pins the pane to the bottom on every new line, with no
  // animation (DESIGN.md).
  useEffect(() => {
    const pane = paneRef.current;
    if (follow && pane) pane.scrollTop = pane.scrollHeight;
  }, [follow, lines.length]);

  const onScroll = () => {
    const pane = paneRef.current;
    if (!pane) return;
    setScrollTop(pane.scrollTop);
    const atBottom =
      pane.scrollHeight - pane.scrollTop - pane.clientHeight < ROW_HEIGHT;
    // Scrolling away releases follow; returning to the bottom re-arms it.
    setFollow(atBottom);
  };

  const total = lines.length;
  const first = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const visibleCount = Math.ceil(PANE_HEIGHT / ROW_HEIGHT) + 2 * OVERSCAN;
  const slice = lines.slice(first, first + visibleCount);

  const searching = query.trim() !== "";

  return (
    <div className="mt-2">
      <div className="flex items-center gap-3">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search logs"
          aria-label="Search logs"
          className="w-64 rounded border border-border bg-surface px-2 py-1 text-sm text-foreground placeholder:text-muted"
        />
        <span className="text-sm text-muted">
          {searching
            ? `${total} matching line${total === 1 ? "" : "s"}`
            : `${total} line${total === 1 ? "" : "s"}`}
        </span>
        <button
          type="button"
          onClick={() => {
            setFollow(true);
            const pane = paneRef.current;
            if (pane) pane.scrollTop = pane.scrollHeight;
          }}
          aria-pressed={follow}
          className={`ml-auto rounded border px-2 py-1 text-sm font-semibold ${
            follow
              ? "border-accent text-accent"
              : "border-border text-muted hover:bg-surface"
          }`}
        >
          Follow
        </button>
      </div>
      <div
        ref={paneRef}
        onScroll={onScroll}
        role="log"
        aria-label="Terminal output"
        className="mt-2 overflow-y-auto rounded border border-border bg-background font-mono text-sm"
        style={{ height: PANE_HEIGHT }}
      >
        {total === 0 ? (
          <p className="px-3 py-4 text-muted">
            {searching
              ? "No lines match."
              : "No command output yet. The transcript appears when the agent runs commands."}
          </p>
        ) : (
          <div style={{ height: total * ROW_HEIGHT, position: "relative" }}>
            <div
              style={{
                position: "absolute",
                top: first * ROW_HEIGHT,
                left: 0,
                right: 0,
              }}
            >
              {slice.map((line) => (
                <LogRow key={line.key} line={line} />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function LogRow({ line }: { line: LogLine }) {
  if (line.redacted) {
    return (
      <div
        style={{ height: ROW_HEIGHT }}
        className="flex items-center gap-2 bg-surface px-3 leading-5"
      >
        <span className="rounded bg-border px-1 text-sm text-muted">
          redacted
        </span>
      </div>
    );
  }
  const toneClass =
    line.stream === "command"
      ? "text-foreground font-semibold"
      : line.stream === "stderr"
        ? "text-danger"
        : line.stream === "system"
          ? "text-muted"
          : "text-foreground";
  return (
    <div
      style={{ height: ROW_HEIGHT }}
      className={`truncate px-3 leading-5 whitespace-pre ${toneClass}`}
    >
      {line.text || " "}
    </div>
  );
}
