"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { api, type Category, type RowDiff, type TableResult } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { Alert, Button, Checkbox, cx, Empty, Loading, Spinner } from "../ui";

function Value({ value }: { value: string | null | undefined }) {
  if (value === null || value === undefined) return <span className="italic text-gray-400">NULL</span>;
  if (value === "") return <span className="italic text-gray-400">(empty)</span>;
  return <>{value}</>;
}

/**
 * Stored row differences of one category. Key columns come first; for
 * differing rows every other column shows the source value above the target
 * value, and changed cells are highlighted.
 */
export function RowsGrid({ runId, table, category }: { runId: string; table: TableResult; category: Category }) {
  const [changedOnly, setChangedOnly] = useState(true);
  const rows = useInfiniteQuery({
    queryKey: ["rows", runId, table.table_name, category],
    queryFn: ({ pageParam }) => api.listRows(runId, table.table_name, category, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_after > 0 ? last.next_after : undefined),
  });

  const keyCount = table.key_columns.length;
  const valueColumns = table.columns.slice(keyCount);
  const loaded: RowDiff[] = useMemo(() => rows.data?.pages.flatMap((p) => p.rows) ?? [], [rows.data]);

  const visibleColumns = useMemo(() => {
    if (category !== "different" || !changedOnly) return valueColumns.map((c, i) => ({ name: c, index: keyCount + i }));
    const changed = new Set(loaded.flatMap((r) => r.diff_columns));
    return valueColumns
      .map((c, i) => ({ name: c, index: keyCount + i }))
      .filter((c) => changed.has(c.name));
  }, [category, changedOnly, valueColumns, keyCount, loaded]);

  if (rows.isPending) return <Loading />;
  if (rows.error) return <Alert>{errorMessage(rows.error)}</Alert>;
  if (loaded.length === 0) return <Empty>No rows in this category.</Empty>;

  const side = category === "only_target" ? "target" : "source";

  return (
    <div className="space-y-3">
      {category === "different" && (
        <div className="flex items-center justify-between">
          <Checkbox label="Show only changed columns" checked={changedOnly} onChange={setChangedOnly} />
          <div className="flex gap-3 text-xs text-gray-500">
            <span>
              <span className="mr-1 inline-block rounded bg-gray-100 px-1 font-semibold">S</span>source
            </span>
            <span>
              <span className="mr-1 inline-block rounded bg-gray-100 px-1 font-semibold">T</span>target
            </span>
            <span>
              <span className="mr-1 inline-block h-3 w-3 rounded-sm bg-amber-200 align-middle" />
              changed
            </span>
          </div>
        </div>
      )}
      <div className="max-h-[70vh] overflow-auto rounded-md border border-gray-200">
        <table className="min-w-full border-collapse text-xs">
          <thead className="sticky top-0 z-10 bg-gray-50 text-left text-gray-600">
            <tr>
              {table.key_columns.map((k) => (
                <th key={k} className="whitespace-nowrap border-b border-r border-gray-200 bg-blue-50 px-2 py-1.5 font-semibold">
                  {k} <span className="font-normal text-blue-600">key</span>
                </th>
              ))}
              {category === "different" && (
                <th className="border-b border-r border-gray-200 px-1 py-1.5" aria-label="side" />
              )}
              {visibleColumns.map((c) => (
                <th key={c.name} className="whitespace-nowrap border-b border-r border-gray-200 px-2 py-1.5 font-medium">
                  {c.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {loaded.map((row) =>
              category === "different" ? (
                <DifferentRow key={row.id} row={row} columns={visibleColumns} />
              ) : (
                <tr key={row.id} className="hover:bg-gray-50">
                  {row.key.map((k, i) => (
                    <td key={i} className="whitespace-nowrap border-b border-r border-gray-100 bg-blue-50/40 px-2 py-1 font-mono">
                      {k}
                    </td>
                  ))}
                  {visibleColumns.map((c) => (
                    <td key={c.name} className="max-w-xs truncate border-b border-r border-gray-100 px-2 py-1 font-mono">
                      <Value value={row[side]?.[c.index]} />
                    </td>
                  ))}
                </tr>
              ),
            )}
          </tbody>
        </table>
      </div>
      <div className="flex items-center justify-between text-xs text-gray-500">
        <span>{loaded.length} rows loaded</span>
        {rows.hasNextPage && (
          <Button onClick={() => rows.fetchNextPage()} disabled={rows.isFetchingNextPage}>
            {rows.isFetchingNextPage && <Spinner />} Load more
          </Button>
        )}
      </div>
    </div>
  );
}

function DifferentRow({ row, columns }: { row: RowDiff; columns: { name: string; index: number }[] }) {
  const changed = new Set(row.diff_columns);
  const cell = (name: string) =>
    cx(
      "max-w-xs truncate border-r border-gray-100 px-2 py-1 font-mono",
      changed.has(name) && "bg-amber-100 text-amber-950",
    );
  return (
    <>
      <tr className="group">
        {row.key.map((k, i) => (
          <td
            key={i}
            rowSpan={2}
            className="whitespace-nowrap border-b border-r border-gray-200 bg-blue-50/40 px-2 py-1 align-top font-mono"
          >
            {k}
          </td>
        ))}
        <td className="border-r border-gray-100 px-1 py-1 text-center font-semibold text-gray-400">S</td>
        {columns.map((c) => (
          <td key={c.name} className={cell(c.name)} title={row.source?.[c.index] ?? "NULL"}>
            <Value value={row.source?.[c.index]} />
          </td>
        ))}
      </tr>
      <tr>
        <td className="border-b border-r border-gray-200 px-1 py-1 text-center font-semibold text-gray-400">T</td>
        {columns.map((c) => (
          <td key={c.name} className={cx(cell(c.name), "border-b border-gray-200")} title={row.target?.[c.index] ?? "NULL"}>
            <Value value={row.target?.[c.index]} />
          </td>
        ))}
      </tr>
    </>
  );
}
