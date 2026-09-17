"use client";

import { diffLines } from "diff";
import { useMemo } from "react";
import { cx } from "./ui";

type Line = { text: string; kind: "same" | "removed" | "added" | "blank" };

function splitLines(value: string): string[] {
  const lines = value.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** Side-by-side line diff of two definitions (source left, target right). */
export function DdlDiff({ source, target }: { source: string; target: string }) {
  const rows = useMemo(() => {
    const left: Line[] = [];
    const right: Line[] = [];
    const changes = diffLines(source, target);
    for (let i = 0; i < changes.length; i++) {
      const c = changes[i];
      if (c.removed) {
        const removed = splitLines(c.value);
        const next = changes[i + 1];
        const added = next?.added ? splitLines(next.value) : [];
        if (next?.added) i++;
        const n = Math.max(removed.length, added.length);
        for (let j = 0; j < n; j++) {
          left.push(j < removed.length ? { text: removed[j], kind: "removed" } : { text: "", kind: "blank" });
          right.push(j < added.length ? { text: added[j], kind: "added" } : { text: "", kind: "blank" });
        }
      } else if (c.added) {
        for (const text of splitLines(c.value)) {
          left.push({ text: "", kind: "blank" });
          right.push({ text, kind: "added" });
        }
      } else {
        for (const text of splitLines(c.value)) {
          left.push({ text, kind: "same" });
          right.push({ text, kind: "same" });
        }
      }
    }
    return left.map((l, i) => [l, right[i]] as const);
  }, [source, target]);

  const cell = (line: Line) =>
    cx(
      "whitespace-pre-wrap break-all px-2 align-top",
      line.kind === "removed" && "bg-red-50 text-red-900",
      line.kind === "added" && "bg-green-50 text-green-900",
      line.kind === "blank" && "bg-gray-50",
    );

  return (
    <div className="overflow-x-auto rounded-md border border-gray-200">
      <table className="w-full table-fixed border-collapse font-mono text-xs leading-5">
        <thead>
          <tr className="bg-gray-50 text-left text-gray-600">
            <th className="w-1/2 border-b border-r border-gray-200 px-2 py-1 font-medium">Source</th>
            <th className="w-1/2 border-b border-gray-200 px-2 py-1 font-medium">Target</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([l, r], i) => (
            <tr key={i}>
              <td className={cx(cell(l), "border-r border-gray-200")}>{l.text}</td>
              <td className={cell(r)}>{r.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
