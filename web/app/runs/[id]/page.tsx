"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { DataTab } from "@/components/run/data-tab";
import { EventLog } from "@/components/run/event-log";
import { SchemaTab } from "@/components/run/schema-tab";
import { useEventStream } from "@/components/run/use-event-stream";
import { StatusBadge } from "@/components/status-badge";
import { Alert, Button, Card, Loading, PageHeader, ProgressBar, Spinner, Stat, Tabs } from "@/components/ui";
import { api, type RowDiffProgress, type RunProgress } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { errorMessage, formatDateTime, formatNumber } from "@/lib/format";
import type { StreamEvent } from "@/lib/sse";

const phaseLabels: Record<string, string> = {
  connect: "Connecting",
  inspect: "Reading schemas",
  schema: "Comparing structure",
  data: "Comparing data",
};

export default function RunPage() {
  const { id } = useParams<{ id: string }>();
  const qc = useQueryClient();
  const [tab, setTab] = useState<"schema" | "data">("schema");
  const [progress, setProgress] = useState<RunProgress | null>(null);
  const [rowProgress, setRowProgress] = useState<Record<string, RowDiffProgress>>({});
  const { can } = useMe();

  const run = useQuery({
    queryKey: ["run", id],
    queryFn: () => api.getRun(id),
    refetchInterval: (q) => (q.state.data?.active || q.state.data?.status === "running" ? 3000 : false),
  });

  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: ["run", id] });
    qc.invalidateQueries({ queryKey: ["schema", id] });
    qc.invalidateQueries({ queryKey: ["tables", id] });
  }, [qc, id]);

  const onEvent = useCallback((e: StreamEvent, log: (text: string, tone?: "error" | "warning") => void) => {
    const d = e.data;
    switch (e.name) {
      case "run_started":
        setRowProgress({});
        log(d.resumed ? "Resuming run; finished tables are skipped" : "Run started");
        break;
      case "connected":
        log(`Connected — source ${d.source_version}, target ${d.target_version}`);
        break;
      case "phase":
        setProgress(d);
        log(phaseLabels[d.phase] ?? d.phase);
        break;
      case "schema_done": {
        const c = d.counts ?? {};
        log(
          `Structure compared: ${d.total} objects — ${c.changed ?? 0} changed, ` +
            `${c.only_source ?? 0} only in source, ${c.only_target ?? 0} only in target`,
        );
        break;
      }
      case "data_started":
        setProgress(d);
        log(`Comparing data of ${d.tables_total} tables` + (d.tables_done ? ` (${d.tables_done} already done)` : ""));
        break;
      case "table_started":
      case "progress":
        setProgress(d);
        break;
      case "table_done":
        log(
          `${d.table_name}: ${d.data_status} (${formatNumber(d.source_rows)} / ${formatNumber(d.target_rows)} rows)`,
          d.data_status === "different" ? "warning" : undefined,
        );
        break;
      case "table_error":
        log(`${d.table_name}: ${d.error}`, "error");
        break;
      case "rowdiff_progress":
        setRowProgress((p) => ({ ...p, [d.table]: d }));
        break;
      case "rowdiff_done":
        setRowProgress((p) => {
          const next = { ...p };
          delete next[d.table_name];
          return next;
        });
        log(
          d.rowdiff_status === "error"
            ? `${d.table_name}: row comparison failed — ${d.error}`
            : `${d.table_name}: ${formatNumber(d.different)} different, ${formatNumber(d.only_source)} only in source, ` +
                `${formatNumber(d.only_target)} only in target`,
          d.rowdiff_status === "error" ? "error" : undefined,
        );
        break;
      case "done":
        setProgress(d.progress);
        setRowProgress({});
        log(`Run ${d.status}${d.error ? `: ${d.error}` : ""}`, d.status === "failed" ? "error" : undefined);
        break;
    }
  }, []);

  const stream = useEventStream({ onEvent, onRefresh: refresh });
  const { start, abort } = stream;
  const execute = useCallback(() => start(`/api/runs/${id}/execute`), [start, id]);
  const stop = useCallback(() => {
    abort();
    api.cancelRun(id).catch(() => undefined).finally(refresh);
  }, [abort, id, refresh]);

  const canExecute =
    can("run.execute") && run.data !== undefined && (!run.data.protected || can("connection.protected"));

  // A new run is created as pending and starts as soon as its page is opened
  // by a user who may execute it.
  const autoStarted = useRef(false);
  useEffect(() => {
    if (run.data?.status === "pending" && canExecute && !autoStarted.current) {
      autoStarted.current = true;
      execute();
    }
  }, [run.data?.status, canExecute, execute]);

  if (run.isPending) return <Loading />;
  if (run.error) return <Alert>{errorMessage(run.error)}</Alert>;
  const r = run.data;
  const live = progress ?? r.progress;
  const runningElsewhere = r.active && !stream.streaming;
  const orphaned = r.status === "running" && !r.active && !stream.streaming;
  const canResume = !stream.streaming && !r.active && ["cancelled", "interrupted", "failed", "pending"].includes(r.status);
  const showProgress = stream.streaming || r.active;
  const s = r.summary;

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            Run #{r.id} <StatusBadge status={stream.streaming ? "running" : r.status} />
          </span>
        }
        subtitle={
          <div className="space-y-0.5">
            <div>
              <span className="font-medium text-gray-700">Source:</span> {r.source_label}
            </div>
            <div>
              <span className="font-medium text-gray-700">Target:</span> {r.target_label}
            </div>
            <div>
              Started {formatDateTime(r.started_at)} by {r.created_by}
              {r.finished_at && ` · finished ${formatDateTime(r.finished_at)}`} ·{" "}
              <Link className="text-blue-600 hover:underline" href={`/projects/${r.project_id}`}>
                project
              </Link>
            </div>
          </div>
        }
        actions={
          <>
            {(stream.streaming || r.active) && can("run.execute") && (
              <Button variant="danger" onClick={stop}>
                Stop
              </Button>
            )}
            {(canResume || orphaned) && canExecute && (
              <Button variant="primary" onClick={execute}>
                {r.status === "pending" ? "Start" : "Resume"}
              </Button>
            )}
            {can("run.export") && (
              <a
                className="inline-flex items-center rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-800 hover:bg-gray-50"
                href={`/api/runs/${r.id}/export.xlsx`}
              >
                Export Excel
              </a>
            )}
          </>
        }
      />

      <div className="mb-6 space-y-3">
        {r.error && !stream.streaming && <Alert>{r.error}</Alert>}
        {stream.error && <Alert>{stream.error}</Alert>}
        {runningElsewhere && (
          <Alert tone="info">
            This run is being executed from another browser session. Progress refreshes automatically.
          </Alert>
        )}
        {orphaned && (
          <Alert tone="warning">
            This run is marked as running, but no server process is executing it.
            {canExecute ? " Resume it to continue." : ""}
          </Alert>
        )}
        {r.protected && !canExecute && can("run.execute") && (
          <Alert tone="info">This run uses a protected connection. Only admins can start or resume it.</Alert>
        )}
        {stream.streaming && (
          <Alert tone="info">Keep this page open until the run finishes. Leaving the page stops the run; finished tables are kept.</Alert>
        )}
      </div>

      {showProgress && (
        <Card className="mb-6 space-y-3 p-4">
          <div className="flex items-center justify-between text-sm">
            <span className="flex items-center gap-2 font-medium text-gray-800">
              <Spinner /> {phaseLabels[live.phase] ?? live.phase ?? "Starting"}
            </span>
            <span className="tabular-nums text-gray-600">
              {live.tables_done} / {live.tables_total} tables
            </span>
          </div>
          <ProgressBar value={live.tables_done} max={live.tables_total} />
          {(live.active_tables?.length ?? 0) > 0 && (
            <div className="space-y-1.5">
              {live.active_tables!.map((t) => {
                const rp = rowProgress[t];
                return (
                  <div key={t} className="flex flex-wrap items-center gap-x-3 text-xs text-gray-600">
                    <span className="font-mono text-gray-800">{t}</span>
                    {rp ? (
                      <span className="tabular-nums">
                        rows: chunk {rp.chunks_done}/{rp.chunks_total}, {formatNumber(rp.rows_scanned)} scanned,{" "}
                        {formatNumber(rp.different)} different, {formatNumber(rp.only_source)} only source,{" "}
                        {formatNumber(rp.only_target)} only target
                      </span>
                    ) : (
                      <span>counting and checksumming…</span>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      )}
      {stream.log.length > 0 && (
        <div className="mb-6">
          <EventLog entries={stream.log} />
        </div>
      )}

      <div className="mb-6 grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
        <Stat label="Objects changed" value={s.schema.changed ?? 0} tone={s.schema.changed ? "amber" : "gray"} />
        <Stat label="Objects only source" value={s.schema.only_source ?? 0} tone={s.schema.only_source ? "amber" : "gray"} />
        <Stat label="Objects only target" value={s.schema.only_target ?? 0} tone={s.schema.only_target ? "amber" : "gray"} />
        <Stat label="Tables identical" value={s.data.identical ?? 0} tone="green" />
        <Stat label="Tables different" value={s.data.different ?? 0} tone={s.data.different ? "amber" : "gray"} />
        <Stat label="Rows different" value={formatNumber(s.rows.different)} tone={s.rows.different ? "amber" : "gray"} />
        <Stat
          label="Rows only source"
          value={formatNumber(s.rows.only_source)}
          tone={s.rows.only_source ? "amber" : "gray"}
        />
        <Stat
          label="Rows only target"
          value={formatNumber(s.rows.only_target)}
          tone={s.rows.only_target ? "amber" : "gray"}
        />
      </div>

      <Card>
        <div className="px-4 pt-2">
          <Tabs
            value={tab}
            onChange={setTab}
            items={[
              { value: "schema", label: "Structure" },
              { value: "data", label: "Data" },
            ]}
          />
        </div>
        <div className="p-4">
          {tab === "schema" ? <SchemaTab runId={id} /> : <DataTab runId={id} rowProgress={rowProgress} />}
        </div>
      </Card>
    </>
  );
}
