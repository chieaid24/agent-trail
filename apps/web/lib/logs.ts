import type { ActivityEvent } from "./types";

export type LogStream = "command" | "stdout" | "stderr" | "system";

export interface LogLine {
  key: string;
  stream: LogStream;
  text: string;
  redacted: boolean;
}

function str(
  payload: Record<string, unknown>,
  key: string,
): string | undefined {
  const v = payload[key];
  return typeof v === "string" ? v : undefined;
}

function commandText(payload: Record<string, unknown>): string {
  const command = str(payload, "command") ?? "";
  const args = payload.args;
  if (Array.isArray(args) && args.every((a) => typeof a === "string")) {
    return [command, ...args].join(" ");
  }
  return command;
}

export function deriveLogLines(events: ActivityEvent[]): LogLine[] {
  const lines: LogLine[] = [];
  for (const e of events) {
    const redacted = e.redaction_status === "redacted";
    switch (e.event_type) {
      case "command.started":
        lines.push({
          key: e.id,
          stream: "command",
          text: redacted ? "" : `$ ${commandText(e.payload)}`,
          redacted,
        });
        break;
      case "command.output": {
        if (redacted) {
          lines.push({ key: e.id, stream: "stdout", text: "", redacted });
          break;
        }
        const stream: LogStream =
          str(e.payload, "stream") === "stderr" ? "stderr" : "stdout";
        const chunk = str(e.payload, "chunk") ?? "";
        // trailing newline ends last line, not an empty extra one
        const chunkLines = chunk.replace(/\n$/, "").split("\n");
        for (const [i, text] of chunkLines.entries()) {
          lines.push({ key: `${e.id}:${i}`, stream, text, redacted });
        }
        break;
      }
      case "command.completed": {
        const exit = e.payload.exit_code;
        if (typeof exit === "number" && !redacted) {
          lines.push({
            key: e.id,
            stream: "system",
            text: `exit ${exit}`,
            redacted,
          });
        } else if (redacted) {
          lines.push({ key: e.id, stream: "system", text: "", redacted });
        }
        break;
      }
    }
  }
  return lines;
}

export function filterLogLines(lines: LogLine[], query: string): LogLine[] {
  const q = query.trim().toLowerCase();
  if (q === "") return lines;
  return lines.filter((l) => !l.redacted && l.text.toLowerCase().includes(q));
}
