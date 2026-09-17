"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useMemo, useState } from "react";
import { api, type RowDiffProgress, type TableResult } from "@/lib/api";
import { errorMessage, formatDuration, formatNumber } from "@/lib/format";
import { StatusBadge } from "../status-badge";
import { Alert, cx, Empty, FilterChips, Input, Loading } from "../ui";

type Filter = "differences" | "different" | "identical" | "error" | "needs_key" | "all";

function matches(t: TableResult, filter: Filter): boolean {
  switch (filter) {
    case "differences":
      return t.data_status !== "identical";
    case "needs_key":
      return t.rowdiff_status === "needs_key";
    case "all":
      return true;
    default:
      return t.data_status === filter;
  }
}

export function tableHref(runId: string | number, table: string) {
  return `/runs/${runId}/table?name=${encodeURIComponent(table)}`;
}

export function DataTab({ runId, rowProgress }: { runId: string; rowProgress: Record<string, RowDiffProgress> }) {
  const tables = useQuery({ queryKey: ["tables", runId], queryFn: () => api.listTables(runId) });
  const [filter, setFilter] = useState<Filter>("differences");
  const [search, setSearch] = useState("");

  const all = tables.data ?? [];
  const count = (f: Filter) => all.filter((t) => matches(t, f)).length;
  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return all.filter((t) => matches(t, filter) && (!q || t.table_name.toLowerCase().includes(q)));
  }, [all, filter, search]);

  if (tables.isPending) return <Loading />;
  if (tables.error) return <Alert>{errorMessage(tables.error)}</Alert>;
  if (all.length === 0) return <Empty>No table data has been compared yet.</Empty>;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          value={filter}
          onChange={setFilter}
          items={[
            { value: "differences", label: "Not identical", count: count("differences") },
            { value: "different", label: "Different", count: count("different") },
            { value: "error", label: "Error", count: count("error") },
            { value: "needs_key", label: "Needs key", count: count("needs_key") },
            { value: "identical", label: "Identical", count: count("identical") },
            { value: "all", label: "All", count: all.length },
          ]}
        />
        <Input className="max-w-xs" placeholder="Search tables…" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      {visible.length === 0 ? (
        <Empty>No tables match.</Empty>
      ) : (
        <div className="overflow-x-auto rounded-md border border-gray-200">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th className="px-3 py-2 font-medium">Table</th>
                <th className="px-3 py-2 text-right font-medium">Source rows</th>
                <th className="px-3 py-2 text-right font-medium">Target rows</th>
                <th className="px-3 py-2 font-medium">Data</th>
                <th className="px-3 py-2 font-medium">Row detail</th>
                <th className="px-3 py-2 text-right font-medium">Different</th>
                <th className="px-3 py-2 text-right font-medium">Only source</th>
                <th className="px-3 py-2 text-right font-medium">Only target</th>
                <th className="px-3 py-2 text-right font-medium">Time</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {visible.map((t) => {
                const live = rowProgress[t.table_name];
                const counts = live ?? t;
                const rowsDiffer = t.source_rows !== null && t.target_rows !== null && t.source_rows !== t.target_rows;
                return (
                  <tr key={t.table_name} className="hover:bg-gray-50">
                    <td className="px-3 py-2">
                      <Link className="font-mono text-xs text-blue-700 hover:underline" href={tableHref(runId, t.table_name)}>
                        {t.table_name}
                      </Link>
                      {t.error && <div className="mt-0.5 max-w-md truncate text-xs text-red-600">{t.error}</div>}
                    </td>
                    <td className={cx("px-3 py-2 text-right tabular-nums", rowsDiffer && "font-semibold text-amber-700")}>
                      {formatNumber(t.source_rows)}
                    </td>
                    <td className={cx("px-3 py-2 text-right tabular-nums", rowsDiffer && "font-semibold text-amber-700")}>
                      {formatNumber(t.target_rows)}
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge status={t.data_status} />
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge status={live ? "running" : t.rowdiff_status} />
                      {t.truncated && <span className="ml-1 text-xs text-amber-700">truncated</span>}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{formatNumber(counts.different)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{formatNumber(counts.only_source)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{formatNumber(counts.only_target)}</td>
                    <td className="px-3 py-2 text-right tabular-nums text-gray-500">{formatDuration(t.duration_ms)}</td>
                    <td className="px-3 py-2 text-right">
                      <Link className="text-sm text-blue-600 hover:underline" href={tableHref(runId, t.table_name)}>
                        Detail
                      </Link>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
