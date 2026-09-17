"use client";

import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type Connection, type ConnectionInput, type Engine, type SSLMode } from "@/lib/api";
import { errorMessage } from "@/lib/format";
import { Alert, Button, Checkbox, Field, Input, Select, Spinner } from "./ui";

const defaultPorts: Record<Engine, number> = { mysql: 3306, postgres: 5432 };

export function ConnectionForm({
  connection,
  onSaved,
  onCancel,
}: {
  connection: Connection | null;
  onSaved: (c: Connection) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<ConnectionInput>(() => ({
    name: connection?.name ?? "",
    engine: connection?.engine ?? "mysql",
    host: connection?.host ?? "",
    port: connection?.port ?? 3306,
    database: connection?.database ?? "",
    schema: connection?.schema ?? "",
    username: connection?.username ?? "",
    password: connection ? null : "",
    ssl_mode: connection?.ssl_mode ?? "disable",
    protected: connection?.protected ?? false,
  }));

  const set = <K extends keyof ConnectionInput>(key: K, value: ConnectionInput[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const save = useMutation({
    mutationFn: () => (connection ? api.updateConnection(connection.id, form) : api.createConnection(form)),
    onSuccess: onSaved,
  });
  const test = useMutation({
    mutationFn: () => api.testConnection({ ...form, id: connection?.id }),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Name">
        <Input required value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="e.g. prod-billing" />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Engine">
          <Select
            value={form.engine}
            onChange={(e) => {
              const engine = e.target.value as Engine;
              setForm((f) => ({
                ...f,
                engine,
                port: f.port === defaultPorts[f.engine] ? defaultPorts[engine] : f.port,
              }));
            }}
          >
            <option value="mysql">MySQL</option>
            <option value="postgres">PostgreSQL</option>
          </Select>
        </Field>
        <Field label="SSL mode">
          <Select value={form.ssl_mode} onChange={(e) => set("ssl_mode", e.target.value as SSLMode)}>
            <option value="disable">disable</option>
            <option value="prefer">prefer</option>
            <option value="require">require</option>
            <option value="verify-full">verify-full</option>
          </Select>
        </Field>
      </div>
      <div className="grid grid-cols-[1fr_100px] gap-3">
        <Field label="Host">
          <Input required value={form.host} onChange={(e) => set("host", e.target.value)} />
        </Field>
        <Field label="Port">
          <Input
            required
            type="number"
            min={1}
            max={65535}
            value={form.port}
            onChange={(e) => set("port", Number(e.target.value))}
          />
        </Field>
      </div>
      <div className={form.engine === "postgres" ? "grid grid-cols-2 gap-3" : ""}>
        <Field label="Database">
          <Input required value={form.database} onChange={(e) => set("database", e.target.value)} />
        </Field>
        {form.engine === "postgres" && (
          <Field label="Schema">
            <Input value={form.schema} onChange={(e) => set("schema", e.target.value)} placeholder="public" />
          </Field>
        )}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Username">
          <Input required value={form.username} onChange={(e) => set("username", e.target.value)} autoComplete="off" />
        </Field>
        <Field label="Password" hint={connection && form.password === null ? "Leave empty to keep the current password" : undefined}>
          <Input
            type="password"
            autoComplete="new-password"
            value={form.password ?? ""}
            placeholder={connection?.has_password ? "••••••••" : ""}
            onChange={(e) => set("password", connection && e.target.value === "" ? null : e.target.value)}
          />
        </Field>
      </div>

      <Checkbox
        label="Protected (e.g. production)"
        hint="Only admins can run compares that use this connection. Everyone can still view the results."
        checked={form.protected}
        onChange={(v) => set("protected", v)}
      />

      {test.data && (
        <Alert tone={test.data.ok ? (test.data.warning ? "warning" : "success") : "error"}>
          {test.data.ok ? (
            <>
              Connected to {test.data.version}. {test.data.tables} tables found.
              {test.data.warning && <div className="mt-1">⚠ {test.data.warning}</div>}
            </>
          ) : (
            test.data.error
          )}
        </Alert>
      )}
      {test.error && <Alert>{errorMessage(test.error)}</Alert>}
      {save.error && <Alert>{errorMessage(save.error)}</Alert>}

      <div className="flex justify-between gap-2 pt-2">
        <Button onClick={() => test.mutate()} disabled={test.isPending}>
          {test.isPending && <Spinner />} Test connection
        </Button>
        <div className="flex gap-2">
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={save.isPending}>
            {save.isPending && <Spinner />} Save
          </Button>
        </div>
      </div>
    </form>
  );
}
