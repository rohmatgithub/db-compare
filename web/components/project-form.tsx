"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type Options, type Project, type ProjectInput } from "@/lib/api";
import { errorMessage, splitList } from "@/lib/format";
import { Alert, Button, Checkbox, Field, Input, Select, Spinner, Textarea } from "./ui";

const defaultOptions: Options = {
  include_tables: [],
  exclude_tables: [],
  ignore_columns: [],
  mask_columns: [],
  key_overrides: {},
  check_column_order: false,
  skip_views: false,
  skip_routines: false,
  include_row_diff: false,
  row_diff_limit: 100000,
  chunk_size: 100000,
  parallelism: 4,
};

function formatKeyOverrides(overrides: Record<string, string[]> | null): string {
  return Object.entries(overrides ?? {})
    .map(([table, cols]) => `${table}: ${cols.join(", ")}`)
    .join("\n");
}

function parseKeyOverrides(text: string): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const line of text.split("\n")) {
    const [table, cols] = line.split(":");
    if (!table?.trim() || !cols) continue;
    const list = cols.split(",").map((c) => c.trim()).filter(Boolean);
    if (list.length > 0) out[table.trim()] = list;
  }
  return out;
}

export function ProjectForm({
  project,
  onSaved,
  onCancel,
}: {
  project: Project | null;
  onSaved: (p: Project) => void;
  onCancel?: () => void;
}) {
  const connections = useQuery({ queryKey: ["connections"], queryFn: api.listConnections });
  const initial = { ...defaultOptions, ...project?.options };
  const [name, setName] = useState(project?.name ?? "");
  const [sourceId, setSourceId] = useState(project?.source_connection_id ?? 0);
  const [targetId, setTargetId] = useState(project?.target_connection_id ?? 0);
  const [opts, setOpts] = useState<Options>(initial);
  const [lists, setLists] = useState({
    include_tables: (initial.include_tables ?? []).join("\n"),
    exclude_tables: (initial.exclude_tables ?? []).join("\n"),
    ignore_columns: (initial.ignore_columns ?? []).join("\n"),
    mask_columns: (initial.mask_columns ?? []).join("\n"),
    key_overrides: formatKeyOverrides(initial.key_overrides),
  });

  const setOpt = <K extends keyof Options>(key: K, value: Options[K]) => setOpts((o) => ({ ...o, [key]: value }));
  const setList = (key: keyof typeof lists, value: string) => setLists((l) => ({ ...l, [key]: value }));

  const save = useMutation({
    mutationFn: () => {
      const input: ProjectInput = {
        name,
        source_connection_id: sourceId,
        target_connection_id: targetId,
        options: {
          ...opts,
          include_tables: splitList(lists.include_tables),
          exclude_tables: splitList(lists.exclude_tables),
          ignore_columns: splitList(lists.ignore_columns),
          mask_columns: splitList(lists.mask_columns),
          key_overrides: parseKeyOverrides(lists.key_overrides),
        },
      };
      return project ? api.updateProject(project.id, input) : api.createProject(input);
    },
    onSuccess: onSaved,
  });

  const source = connections.data?.find((c) => c.id === sourceId);
  const targetChoices = connections.data?.filter((c) => c.id !== sourceId && (!source || c.engine === source.engine));

  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="grid gap-4 md:grid-cols-3">
        <Field label="Project name">
          <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. billing prod vs staging" />
        </Field>
        <Field label="Source (DB1)">
          <Select required value={sourceId || ""} onChange={(e) => setSourceId(Number(e.target.value))}>
            <option value="">Select a connection…</option>
            {connections.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.engine})
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Target (DB2)">
          <Select required value={targetId || ""} onChange={(e) => setTargetId(Number(e.target.value))}>
            <option value="">Select a connection…</option>
            {targetChoices?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.engine})
              </option>
            ))}
          </Select>
        </Field>
      </div>
      {connections.data?.length === 0 && (
        <Alert tone="info">Create at least two connections of the same engine first.</Alert>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <Field label="Include tables" hint="One pattern per line, * wildcards allowed. Empty means all tables.">
          <Textarea rows={3} value={lists.include_tables} onChange={(e) => setList("include_tables", e.target.value)} />
        </Field>
        <Field label="Exclude tables" hint="e.g. tmp_*, *_backup">
          <Textarea rows={3} value={lists.exclude_tables} onChange={(e) => setList("exclude_tables", e.target.value)} />
        </Field>
        <Field label="Ignore columns" hint="table.column; a bare column name applies to every table, e.g. updated_at">
          <Textarea rows={3} value={lists.ignore_columns} onChange={(e) => setList("ignore_columns", e.target.value)} />
        </Field>
        <Field label="Mask columns" hint="Values are compared but stored and exported as ***, e.g. *.password, customers.nik">
          <Textarea rows={3} value={lists.mask_columns} onChange={(e) => setList("mask_columns", e.target.value)} />
        </Field>
        <Field label="Key columns per table" hint="For tables without a primary key. One line per table: table: col1, col2">
          <Textarea rows={3} value={lists.key_overrides} onChange={(e) => setList("key_overrides", e.target.value)} />
        </Field>
        <div className="space-y-2.5">
          <Checkbox
            label="Compare rows during the run"
            hint="Otherwise row detail is compared per table on demand."
            checked={opts.include_row_diff}
            onChange={(v) => setOpt("include_row_diff", v)}
          />
          <Checkbox label="Report column order changes" checked={opts.check_column_order} onChange={(v) => setOpt("check_column_order", v)} />
          <Checkbox label="Skip views" checked={opts.skip_views} onChange={(v) => setOpt("skip_views", v)} />
          <Checkbox label="Skip functions and procedures" checked={opts.skip_routines} onChange={(v) => setOpt("skip_routines", v)} />
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Field label="Stored row differences per table" hint="Counting continues past this limit.">
          <Input type="number" min={1} value={opts.row_diff_limit} onChange={(e) => setOpt("row_diff_limit", Number(e.target.value))} />
        </Field>
        <Field label="Chunk size (rows)" hint="Large tables are checksummed per chunk; only changed chunks are read.">
          <Input type="number" min={1000} value={opts.chunk_size} onChange={(e) => setOpt("chunk_size", Number(e.target.value))} />
        </Field>
        <Field label="Tables in parallel">
          <Input type="number" min={1} max={16} value={opts.parallelism} onChange={(e) => setOpt("parallelism", Number(e.target.value))} />
        </Field>
      </div>

      {save.error && <Alert>{errorMessage(save.error)}</Alert>}
      <div className="flex justify-end gap-2">
        {onCancel && (
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" variant="primary" disabled={save.isPending}>
          {save.isPending && <Spinner />} {project ? "Save changes" : "Create project"}
        </Button>
      </div>
    </form>
  );
}
