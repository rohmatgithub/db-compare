"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ConnectionForm } from "@/components/connection-form";
import { Alert, Button, Card, Empty, Loading, PageHeader } from "@/components/ui";
import { api, type Connection } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { errorMessage, formatDateTime } from "@/lib/format";

export default function ConnectionsPage() {
  const qc = useQueryClient();
  const connections = useQuery({ queryKey: ["connections"], queryFn: api.listConnections });
  const { can } = useMe();
  const canManage = can("connection.manage");
  const [editing, setEditing] = useState<Connection | "new" | null>(null);

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteConnection(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connections"] }),
  });

  return (
    <>
      <PageHeader
        title="Connections"
        subtitle="Databases that can be compared. Use read-only database users."
        actions={
          canManage && (
            <Button variant="primary" onClick={() => setEditing("new")}>
              New connection
            </Button>
          )
        }
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_420px]">
        <Card>
          {connections.isPending && <Loading />}
          {connections.error && <Alert>{errorMessage(connections.error)}</Alert>}
          {remove.error && (
            <div className="p-3">
              <Alert>{errorMessage(remove.error)}</Alert>
            </div>
          )}
          {connections.data?.length === 0 && <Empty>No connections yet.</Empty>}
          {connections.data && connections.data.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-gray-200 bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
                  <tr>
                    <th className="px-4 py-2 font-medium">Name</th>
                    <th className="px-4 py-2 font-medium">Engine</th>
                    <th className="px-4 py-2 font-medium">Location</th>
                    <th className="px-4 py-2 font-medium">User</th>
                    <th className="px-4 py-2 font-medium">Updated</th>
                    <th className="px-4 py-2" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {connections.data.map((c) => (
                    <tr key={c.id} className={editing !== "new" && editing?.id === c.id ? "bg-blue-50" : ""}>
                      <td className="px-4 py-2 font-medium text-gray-900">
                        {c.name}
                        {c.protected && (
                          <span
                            className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800"
                            title="Only admins can run compares that use this connection"
                          >
                            Protected
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2">
                        {c.engine}
                        {c.server_version && <div className="text-xs text-gray-500">{c.server_version}</div>}
                      </td>
                      <td className="px-4 py-2 font-mono text-xs">
                        {c.host}:{c.port}/{c.database}
                        {c.engine === "postgres" && `.${c.schema || "public"}`}
                      </td>
                      <td className="px-4 py-2">{c.username}</td>
                      <td className="px-4 py-2 text-gray-500">{formatDateTime(c.updated_at)}</td>
                      <td className="whitespace-nowrap px-4 py-2 text-right">
                        {canManage && (
                          <>
                            <Button variant="ghost" onClick={() => setEditing(c)}>
                              Edit
                            </Button>
                            <Button
                              variant="ghost"
                              className="text-red-600"
                              disabled={remove.isPending}
                              onClick={() => {
                                if (window.confirm(`Delete connection "${c.name}"?`)) remove.mutate(c.id);
                              }}
                            >
                              Delete
                            </Button>
                          </>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        {editing && canManage && (
          <Card className="p-4">
            <h2 className="mb-4 font-semibold text-gray-900">
              {editing === "new" ? "New connection" : `Edit ${editing.name}`}
            </h2>
            <ConnectionForm
              key={editing === "new" ? "new" : editing.id}
              connection={editing === "new" ? null : editing}
              onCancel={() => setEditing(null)}
              onSaved={() => {
                qc.invalidateQueries({ queryKey: ["connections"] });
                setEditing(null);
              }}
            />
          </Card>
        )}
      </div>
    </>
  );
}
