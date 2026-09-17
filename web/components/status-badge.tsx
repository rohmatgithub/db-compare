import { cx } from "./ui";

type Tone = "green" | "amber" | "red" | "blue" | "gray" | "purple";

const tones: Record<Tone, string> = {
  green: "bg-green-50 text-green-700 ring-green-600/20",
  amber: "bg-amber-50 text-amber-800 ring-amber-600/20",
  red: "bg-red-50 text-red-700 ring-red-600/20",
  blue: "bg-blue-50 text-blue-700 ring-blue-600/20",
  gray: "bg-gray-50 text-gray-600 ring-gray-500/20",
  purple: "bg-purple-50 text-purple-700 ring-purple-600/20",
};

const statuses: Record<string, { label: string; tone: Tone }> = {
  // Runs
  pending: { label: "Pending", tone: "gray" },
  running: { label: "Running", tone: "blue" },
  completed: { label: "Completed", tone: "green" },
  cancelled: { label: "Cancelled", tone: "amber" },
  interrupted: { label: "Interrupted", tone: "amber" },
  failed: { label: "Failed", tone: "red" },
  // Schema objects
  same: { label: "Same", tone: "green" },
  changed: { label: "Changed", tone: "amber" },
  only_source: { label: "Only in source", tone: "purple" },
  only_target: { label: "Only in target", tone: "red" },
  // Table data
  identical: { label: "Identical", tone: "green" },
  different: { label: "Different", tone: "amber" },
  error: { label: "Error", tone: "red" },
  // Row comparison
  not_run: { label: "Not run", tone: "gray" },
  not_needed: { label: "Not needed", tone: "gray" },
  needs_key: { label: "Needs key", tone: "purple" },
  done: { label: "Done", tone: "green" },
};

export function StatusBadge({ status, className }: { status: string; className?: string }) {
  const s = statuses[status] ?? { label: status, tone: "gray" as Tone };
  return (
    <span
      className={cx(
        "inline-flex items-center whitespace-nowrap rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset",
        tones[s.tone],
        className,
      )}
    >
      {s.label}
    </span>
  );
}
