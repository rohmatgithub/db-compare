"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { ProjectForm } from "@/components/project-form";
import { StatusBadge } from "@/components/status-badge";
import { Alert, Button, Card, Empty, Loading, PageHeader, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { errorMessage, formatDateTime } from "@/lib/format";

export default function ProjectPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const { can } = useMe();

  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.getProject(id) });
  const connections = useQuery({ queryKey: ["connections"], queryFn: api.listConnections });
  const runs = useQuery({
    queryKey: ["runs", id],
    queryFn: () => api.listRuns(id),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 5000 : false),
  });

  const start = useMutation({
    mutationFn: () => api.createRun(Number(id)),
    onSuccess: (run) => router.push(`/runs/${run.id}`),
  });
  const removeRun = useMutation({
    mutationFn: (runId: number) => api.deleteRun(runId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["runs", id] }),
  });
  const removeProject = useMutation({
    mutationFn: () => api.deleteProject(Number(id)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      router.push("/projects");
    },
  });

  if (project.isPending) return <Loading />;
  if (project.error) return <Alert>{errorMessage(project.error)}</Alert>;
  const p = project.data;
  const conn = (cid: number) => connections.data?.find((c) => c.id === cid);
  const src = conn(p.source_connection_id);
  const tgt = conn(p.target_connection_id);
  const isProtected = Boolean(src?.protected || tgt?.protected);
  const canRun = can("run.execute") && (!isProtected || can("connection.protected"));

  return (
    <>
      <PageHeader
        title={p.name}
        subtitle={
          <span>
            <b>{src?.name ?? "…"}</b> {src && `(${src.host}/${src.database})`} → <b>{tgt?.name ?? "…"}</b>{" "}
            {tgt && `(${tgt.host}/${tgt.database})`}
          </span>
        }
        actions={
          <>
            {can("project.write") && (
              <Button onClick={() => setEditing((v) => !v)}>{editing ? "Close settings" : "Settings"}</Button>
            )}
            {can("project.delete") && (
              <Button
                variant="danger"
                disabled={removeProject.isPending}
                onClick={() => {
                  if (window.confirm(`Delete project "${p.name}" and all its runs?`)) removeProject.mutate();
                }}
              >
                Delete
              </Button>
            )}
            {can("run.execute") && (
              <Button
                variant="primary"
                onClick={() => start.mutate()}
                disabled={start.isPending || !canRun}
                title={canRun ? undefined : "Only admins can run compares on protected connections"}
              >
                {start.isPending && <Spinner />} Compare now
              </Button>
            )}
          </>
        }
      />
      {isProtected && (
        <div className="mb-4">
          <Alert tone="warning">
            This project uses a protected connection.{" "}
            {canRun ? "Compares here run against a protected database." : "Only admins can run compares for it."}
          </Alert>
        </div>
      )}
      {start.error && (
        <div className="mb-4">
          <Alert>{errorMessage(start.error)}</Alert>
        </div>
      )}
      {removeProject.error && (
        <div className="mb-4">
          <Alert>{errorMessage(removeProject.error)}</Alert>
        </div>
      )}

      {editing && can("project.write") && (
        <Card className="mb-6 p-5">
          <ProjectForm
            project={p}
            onCancel={() => setEditing(false)}
            onSaved={() => {
              qc.invalidateQueries({ queryKey: ["project", id] });
              qc.invalidateQueries({ queryKey: ["projects"] });
              setEditing(false);
            }}
          />
        </Card>
      )}

      <Card>
        <div className="border-b border-gray-200 px-4 py-3 font-semibold text-gray-900">Run history</div>
        {runs.isPending && <Loading />}
        {runs.error && <Alert>{errorMessage(runs.error)}</Alert>}
        {removeRun.error && <Alert>{errorMessage(removeRun.error)}</Alert>}
        {runs.data?.length === 0 && (
          <Empty>{canRun ? "No runs yet. Click “Compare now” to start one." : "No runs yet."}</Empty>
        )}
        {runs.data && runs.data.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
                <tr>
                  <th className="px-4 py-2 font-medium">Run</th>
                  <th className="px-4 py-2 font-medium">Status</th>
                  <th className="px-4 py-2 font-medium">Tables</th>
                  <th className="px-4 py-2 font-medium">Started by</th>
                  <th className="px-4 py-2 font-medium">Started</th>
                  <th className="px-4 py-2 font-medium">Finished</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {runs.data.map((r) => (
                  <tr
                    key={r.id}
                    className="cursor-pointer hover:bg-gray-50"
                    onClick={(e) => {
                      // Links and buttons inside the row keep their own action.
                      if ((e.target as HTMLElement).closest("a, button")) return;
                      router.push(`/runs/${r.id}`);
                    }}
                  >
                    <td className="px-4 py-2">
                      <Link className="font-medium text-blue-600 hover:underline" href={`/runs/${r.id}`}>
                        #{r.id}
                      </Link>
                    </td>
                    <td className="px-4 py-2">
                      <StatusBadge status={r.status} />
                      {r.error && <div className="mt-1 max-w-md truncate text-xs text-red-600">{r.error}</div>}
                    </td>
                    <td className="px-4 py-2 tabular-nums">
                      {r.progress.tables_total > 0 ? `${r.progress.tables_done} / ${r.progress.tables_total}` : "–"}
                    </td>
                    <td className="px-4 py-2">{r.created_by}</td>
                    <td className="px-4 py-2 text-gray-600">{formatDateTime(r.started_at)}</td>
                    <td className="px-4 py-2 text-gray-600">{formatDateTime(r.finished_at)}</td>
                    <td className="whitespace-nowrap px-4 py-2 text-right">
                      {can("run.export") && (
                        <a className="mr-2 text-sm text-blue-600 hover:underline" href={`/api/runs/${r.id}/export.xlsx`}>
                          Excel
                        </a>
                      )}
                      {can("run.delete") && (
                        <Button
                          variant="ghost"
                          className="text-red-600"
                          disabled={r.status === "running" || removeRun.isPending}
                          onClick={() => {
                            if (window.confirm(`Delete run #${r.id} and its results?`)) removeRun.mutate(r.id);
                          }}
                        >
                          Delete
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}
