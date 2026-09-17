"use client";

import { useQuery } from "@tanstack/react-query";
import { Fragment, useMemo, useState } from "react";
import { api, type ObjectStatus, type SchemaItem } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { DdlDiff } from "../ddl-diff";
import { StatusBadge } from "../status-badge";
import { Alert, Empty, FilterChips, Input, Loading } from "../ui";

type Filter = "differences" | ObjectStatus | "all";

export function SchemaTab({ runId }: { runId: string }) {
  const items = useQuery({ queryKey: ["schema", runId], queryFn: () => api.listSchemaItems(runId) });
  const [filter, setFilter] = useState<Filter>("differences");
  const [search, setSearch] = useState("");
  const [open, setOpen] = useState<number | null>(null);

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const it of items.data ?? []) c[it.status] = (c[it.status] ?? 0) + 1;
    return c;
  }, [items.data]);

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (items.data ?? []).filter((it) => {
      if (filter === "differences" && it.status === "same") return false;
      if (filter !== "differences" && filter !== "all" && it.status !== filter) return false;
      return !q || it.name.toLowerCase().includes(q);
    });
  }, [items.data, filter, search]);

  if (items.isPending) return <Loading />;
  if (items.error) return <Alert>{errorMessage(items.error)}</Alert>;
  if (items.data.length === 0) return <Empty>The structure has not been compared yet.</Empty>;

  const total = items.data.length;
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          value={filter}
          onChange={setFilter}
          items={[
            { value: "differences", label: "Differences", count: total - (counts.same ?? 0) },
            { value: "changed", label: "Changed", count: counts.changed ?? 0 },
            { value: "only_source", label: "Only in source", count: counts.only_source ?? 0 },
            { value: "only_target", label: "Only in target", count: counts.only_target ?? 0 },
            { value: "same", label: "Same", count: counts.same ?? 0 },
            { value: "all", label: "All", count: total },
          ]}
        />
        <Input className="max-w-xs" placeholder="Search objects…" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      {visible.length === 0 ? (
        <Empty>No objects match.</Empty>
      ) : (
        <div className="overflow-x-auto rounded-md border border-gray-200">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th className="px-3 py-2 font-medium">Type</th>
                <th className="px-3 py-2 font-medium">Name</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 font-medium">Changes</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {visible.map((it) => (
                <Fragment key={it.id}>
                  <tr
                    className="cursor-pointer hover:bg-gray-50"
                    onClick={() => setOpen((o) => (o === it.id ? null : it.id))}
                  >
                    <td className="px-3 py-2 text-gray-600">{it.object_type.replace("_", " ")}</td>
                    <td className="px-3 py-2 font-mono text-xs text-gray-900">
                      <span className="mr-1 inline-block w-3 text-gray-400">{open === it.id ? "▾" : "▸"}</span>
                      {it.name}
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge status={it.status} />
                    </td>
                    <td className="px-3 py-2 text-gray-600">{summarizeChanges(it)}</td>
                  </tr>
                  {open === it.id && (
                    <tr>
                      <td colSpan={4} className="bg-gray-50 px-3 py-3">
                        <SchemaItemDetail runId={runId} item={it} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function summarizeChanges(it: SchemaItem): string {
  const changes = it.changes ?? [];
  if (changes.length === 0) return "";
  const byKind: Record<string, number> = {};
  for (const c of changes) byKind[c.kind] = (byKind[c.kind] ?? 0) + 1;
  return Object.entries(byKind)
    .map(([kind, n]) => `${n} ${kind.replace("_", " ")}${n > 1 ? "s" : ""}`)
    .join(", ");
}

function SchemaItemDetail({ runId, item }: { runId: string; item: SchemaItem }) {
  const detail = useQuery({
    queryKey: ["schema-item", runId, item.id],
    queryFn: () => api.getSchemaItem(runId, item.id),
  });
  if (detail.isPending) return <Loading />;
  if (detail.error) return <Alert>{errorMessage(detail.error)}</Alert>;
  const d = detail.data;
  const changes = d.changes ?? [];

  return (
    <div className="space-y-3">
      {changes.length > 0 && (
        <table className="w-full rounded-md border border-gray-200 bg-white text-xs">
          <thead className="text-left text-gray-500">
            <tr>
              <th className="px-2 py-1.5 font-medium">Kind</th>
              <th className="px-2 py-1.5 font-medium">Name</th>
              <th className="px-2 py-1.5 font-medium">Status</th>
              <th className="px-2 py-1.5 font-medium">Source</th>
              <th className="px-2 py-1.5 font-medium">Target</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {changes.map((c, i) => (
              <tr key={i}>
                <td className="px-2 py-1.5">{c.kind.replace("_", " ")}</td>
                <td className="px-2 py-1.5 font-mono">{c.name}</td>
                <td className="px-2 py-1.5">
                  <StatusBadge status={c.status} />
                </td>
                <td className="px-2 py-1.5 font-mono text-gray-700">{c.source ?? "–"}</td>
                <td className="px-2 py-1.5 font-mono text-gray-700">{c.target ?? "–"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {d.source_ddl || d.target_ddl ? (
        <DdlDiff source={d.source_ddl ?? ""} target={d.target_ddl ?? ""} />
      ) : (
        <div className="text-xs text-gray-500">No definition available (the user may lack privileges to read it).</div>
      )}
    </div>
  );
}
