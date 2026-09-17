"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useCallback, useState } from "react";
import { api, tablePath, type Category, type RowDiffProgress } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { errorMessage, formatDuration, formatNumber } from "@/lib/format";
import type { StreamEvent } from "@/lib/sse";
import { StatusBadge } from "../status-badge";
import { Alert, Button, Card, Checkbox, Loading, PageHeader, ProgressBar, Spinner, Stat, Tabs } from "../ui";
import { EventLog } from "./event-log";
import { RowsGrid } from "./rows-grid";
import { useEventStream } from "./use-event-stream";

const phaseLabels: Record<string, string> = {
  connect: "Connecting",
  inspect: "Reading table structure",
  summary: "Counting rows",
  rowdiff: "Comparing rows",
};

export function TableDetail({ runId, name }: { runId: string; name: string }) {
  const qc = useQueryClient();
  const run = useQuery({ queryKey: ["run", runId], queryFn: () => api.getRun(runId) });
  const table = useQuery({ queryKey: ["table", runId, name], queryFn: () => api.getTable(runId, name) });
  const [category, setCategory] = useState<Category | null>(null);
  const [phase, setPhase] = useState<string | null>(null);
  const [progress, setProgress] = useState<RowDiffProgress | null>(null);
  const [choosingKey, setChoosingKey] = useState(false);
  const [keyColumns, setKeyColumns] = useState<string[]>([]);
  const { can } = useMe();

  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: ["table", runId, name] });
    qc.invalidateQueries({ queryKey: ["rows", runId, name] });
    qc.invalidateQueries({ queryKey: ["tables", runId] });
    qc.invalidateQueries({ queryKey: ["run", runId] });
  }, [qc, runId, name]);

  const onEvent = useCallback((e: StreamEvent, log: (text: string, tone?: "error" | "warning") => void) => {
    const d = e.data;
    switch (e.name) {
      case "phase":
        setPhase(d.phase);
        log(phaseLabels[d.phase] ?? d.phase);
        break;
      case "rowdiff_progress":
        setProgress(d);
        break;
      case "rowdiff_done":
        setProgress(null);
        if (d.rowdiff_status === "needs_key") log("This table has no usable key; choose key columns.", "warning");
        else if (d.rowdiff_status === "error") log(`Row comparison failed: ${d.error}`, "error");
        else
          log(
            `Finished: ${formatNumber(d.different)} different, ${formatNumber(d.only_source)} only in source, ` +
              `${formatNumber(d.only_target)} only in target`,
          );
        break;
      case "done":
        setPhase(null);
        break;
    }
  }, []);

  const stream = useEventStream({ onEvent, onRefresh: refresh });

  if (run.isPending || table.isPending) return <Loading />;
  if (run.error) return <Alert>{errorMessage(run.error)}</Alert>;
  if (table.error) return <Alert>{errorMessage(table.error)}</Alert>;
  const r = run.data;
  const t = table.data;

  const canCompare = can("run.execute") && (!r.protected || can("connection.protected"));
  const busyElsewhere = !stream.streaming && (r.active || r.status === "running");
  const needsKey = t.rowdiff_status === "needs_key";
  const hasRows = ["done", "cancelled", "interrupted", "error"].includes(t.rowdiff_status);
  const counts = { different: t.different, only_source: t.only_source, only_target: t.only_target };
  const activeCategory: Category =
    category ?? (counts.different > 0 ? "different" : counts.only_source > 0 ? "only_source" : counts.only_target > 0 ? "only_target" : "different");

  const compare = () => {
    setProgress(null);
    stream.start(`${tablePath(runId, name)}/rowdiff`, {
      key_columns: choosingKey && keyColumns.length > 0 ? keyColumns : undefined,
    });
    setChoosingKey(false);
  };
  const stop = () => {
    stream.abort();
    api.cancelRun(runId).catch(() => undefined).finally(refresh);
  };

  const keyChoices = t.columns.length > 0 ? t.columns : [];
  const rowsScanned = progress?.rows_scanned ?? 0;
  const totalRows = Math.max(t.source_rows ?? 0, t.target_rows ?? 0);

  return (
    <>
      <PageHeader
        title={<span className="font-mono">{t.table_name}</span>}
        subtitle={
          <span>
            <Link className="text-blue-600 hover:underline" href={`/runs/${runId}`}>
              Run #{runId}
            </Link>{" "}
            · {r.source_label} → {r.target_label}
          </span>
        }
        actions={
          <>
            {stream.streaming ? (
              <Button variant="danger" onClick={stop}>
                Stop
              </Button>
            ) : (
              canCompare && (
                <>
                  <Button onClick={() => setChoosingKey((v) => !v)}>{choosingKey ? "Cancel key choice" : "Choose key"}</Button>
                  <Button
                    variant="primary"
                    onClick={compare}
                    disabled={busyElsewhere || (needsKey && !(choosingKey && keyColumns.length > 0))}
                  >
                    {hasRows ? "Compare rows again" : "Compare rows"}
                  </Button>
                </>
              )
            )}
            {hasRows && can("run.export") && (
              <a
                className="inline-flex items-center rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-800 hover:bg-gray-50"
                href={`${tablePath(runId, name)}/export.xlsx`}
              >
                Export Excel
              </a>
            )}
          </>
        }
      />

      <div className="mb-6 space-y-3">
        {busyElsewhere && <Alert tone="info">The run is in progress. Row comparison is available after it finishes.</Alert>}
        {t.error && <Alert>{t.error}</Alert>}
        {stream.error && <Alert>{stream.error}</Alert>}
        {needsKey && !choosingKey && (
          <Alert tone="warning">
            This table has no primary key or usable unique index on both sides.
            {canCompare ? " Choose the key columns that identify a row." : ""}
          </Alert>
        )}
        {r.protected && !canCompare && can("run.execute") && (
          <Alert tone="info">This run uses a protected connection. Only admins can compare its rows.</Alert>
        )}
        {t.truncated && (
          <Alert tone="warning">
            Only the first {formatNumber(r.options.row_diff_limit)} differing rows are stored. The counts below include all rows.
          </Alert>
        )}
        {t.notes.length > 0 && (
          <Alert tone="info">
            <ul className="list-inside list-disc">
              {t.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          </Alert>
        )}
      </div>

      {choosingKey && canCompare && !stream.streaming && (
        <Card className="mb-6 p-4">
          <div className="mb-2 text-sm font-medium text-gray-800">Key columns (in order)</div>
          <div className="grid grid-cols-2 gap-2 md:grid-cols-4 lg:grid-cols-6">
            {keyChoices.map((c) => (
              <Checkbox
                key={c}
                label={keyColumns.includes(c) ? `${c} (${keyColumns.indexOf(c) + 1})` : c}
                checked={keyColumns.includes(c)}
                onChange={(on) => setKeyColumns((k) => (on ? [...k, c] : k.filter((x) => x !== c)))}
              />
            ))}
          </div>
          <div className="mt-2 text-xs text-gray-500">
            The combination must be unique and never NULL on both sides. It applies to this comparison only; add it to the
            project settings to use it in future runs.
          </div>
        </Card>
      )}

      {stream.streaming && (
        <Card className="mb-6 space-y-3 p-4">
          <div className="flex items-center justify-between text-sm">
            <span className="flex items-center gap-2 font-medium text-gray-800">
              <Spinner /> {phaseLabels[phase ?? ""] ?? "Starting"}
            </span>
            {progress && (
              <span className="tabular-nums text-gray-600">
                chunk {progress.chunks_done}/{progress.chunks_total} · {formatNumber(rowsScanned)} rows scanned
              </span>
            )}
          </div>
          <ProgressBar value={rowsScanned} max={totalRows} />
          {progress && (
            <div className="text-xs tabular-nums text-gray-600">
              {formatNumber(progress.different)} different · {formatNumber(progress.only_source)} only in source ·{" "}
              {formatNumber(progress.only_target)} only in target
            </div>
          )}
        </Card>
      )}
      {stream.log.length > 0 && (
        <div className="mb-6">
          <EventLog entries={stream.log} />
        </div>
      )}

      <div className="mb-6 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Stat label="Source rows" value={formatNumber(t.source_rows)} />
        <Stat label="Target rows" value={formatNumber(t.target_rows)} />
        <Stat label="Different" value={formatNumber(t.different)} tone={t.different ? "amber" : "gray"} />
        <Stat label="Only in source" value={formatNumber(t.only_source)} tone={t.only_source ? "amber" : "gray"} />
        <Stat label="Only in target" value={formatNumber(t.only_target)} tone={t.only_target ? "amber" : "gray"} />
        <Stat label="Identical rows" value={formatNumber(t.identical)} tone="green" />
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-3 text-sm text-gray-600">
        <span>
          Data <StatusBadge status={t.data_status} />
        </span>
        <span>
          Row detail <StatusBadge status={stream.streaming ? "running" : t.rowdiff_status} />
        </span>
        <span>
          Key: <span className="font-mono">{t.key_columns.length > 0 ? t.key_columns.join(", ") : "none"}</span>
        </span>
        <span>Time: {formatDuration(t.duration_ms)}</span>
      </div>

      {hasRows ? (
        <Card>
          <div className="px-4 pt-2">
            <Tabs
              value={activeCategory}
              onChange={setCategory}
              items={[
                { value: "different", label: `Different (${formatNumber(counts.different)})` },
                { value: "only_source", label: `Only in source (${formatNumber(counts.only_source)})` },
                { value: "only_target", label: `Only in target (${formatNumber(counts.only_target)})` },
              ]}
            />
          </div>
          <div className="p-4">
            <RowsGrid runId={runId} table={t} category={activeCategory} />
          </div>
        </Card>
      ) : (
        !stream.streaming && (
          <Card className="p-6 text-center text-sm text-gray-500">
            {t.data_status === "identical"
              ? "The table data is identical on both sides."
              : canCompare
                ? "Rows have not been compared yet. Click “Compare rows” to list the differing rows."
                : "Rows have not been compared yet."}
          </Card>
        )
      )}
    </>
  );
}
