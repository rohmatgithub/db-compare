"use client";

import { useEffect, useRef } from "react";
import { cx } from "../ui";
import type { LogEntry } from "./use-event-stream";

export function EventLog({ entries }: { entries: LogEntry[] }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [entries]);

  if (entries.length === 0) return null;
  return (
    <div ref={ref} className="max-h-48 overflow-y-auto rounded-md bg-gray-900 p-3 font-mono text-xs leading-5 text-gray-200">
      {entries.map((e, i) => (
        <div
          key={i}
          className={cx(e.tone === "error" && "text-red-300", e.tone === "warning" && "text-amber-300")}
        >
          <span className="text-gray-500">{e.time.toLocaleTimeString("en-GB")}</span> {e.text}
        </div>
      ))}
    </div>
  );
}
