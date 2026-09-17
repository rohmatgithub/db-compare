"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "@/lib/format";
import { streamEvents, type StreamEvent } from "@/lib/sse";

export type LogEntry = { time: Date; text: string; tone?: "error" | "warning" };

const maxLogEntries = 300;
const refreshInterval = 1500;

/**
 * Runs one streaming request at a time and exposes its state. onEvent maps
 * events to log lines; onRefresh is called at most every refreshInterval
 * while events arrive, and once when the stream ends.
 */
export function useEventStream({
  onEvent,
  onRefresh,
}: {
  onEvent: (event: StreamEvent, log: (text: string, tone?: LogEntry["tone"]) => void) => void;
  onRefresh: () => void;
}) {
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [log, setLog] = useState<LogEntry[]>([]);
  const abortRef = useRef<AbortController | null>(null);
  const handlers = useRef({ onEvent, onRefresh });
  handlers.current = { onEvent, onRefresh };
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const scheduleRefresh = useCallback(() => {
    if (refreshTimer.current) return;
    refreshTimer.current = setTimeout(() => {
      refreshTimer.current = null;
      handlers.current.onRefresh();
    }, refreshInterval);
  }, []);

  const addLog = useCallback((text: string, tone?: LogEntry["tone"]) => {
    setLog((l) => [...l.slice(-(maxLogEntries - 1)), { time: new Date(), text, tone }]);
  }, []);

  const start = useCallback(
    async (url: string, body?: unknown) => {
      if (abortRef.current) return;
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setStreaming(true);
      setError(null);
      try {
        await streamEvents(
          url,
          body,
          (event) => {
            if (event.name === "heartbeat") return;
            if (event.name === "error") {
              const message = event.data?.message ?? String(event.data);
              setError(message);
              addLog(message, "error");
            }
            handlers.current.onEvent(event, addLog);
            scheduleRefresh();
          },
          ctrl.signal,
        );
      } catch (err) {
        if (!ctrl.signal.aborted) {
          setError(errorMessage(err));
          addLog(errorMessage(err), "error");
        }
      } finally {
        abortRef.current = null;
        setStreaming(false);
        handlers.current.onRefresh();
      }
    },
    [addLog, scheduleRefresh],
  );

  const abort = useCallback(() => abortRef.current?.abort(), []);

  useEffect(
    () => () => {
      abortRef.current?.abort();
      if (refreshTimer.current) clearTimeout(refreshTimer.current);
    },
    [],
  );

  // Leaving the page closes the stream, which stops the work on the server.
  useEffect(() => {
    if (!streaming) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [streaming]);

  return { streaming, error, log, start, abort, addLog };
}
