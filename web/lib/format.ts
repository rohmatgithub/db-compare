const numberFormat = new Intl.NumberFormat("en-US");

export function formatNumber(n: number | null | undefined): string {
  return n === null || n === undefined ? "–" : numberFormat.format(n);
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return "–";
  return new Date(value).toLocaleString("en-GB", {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  return `${m} m ${s % 60} s`;
}

export function splitList(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
