"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Alert, Card, Empty, LinkButton, Loading, PageHeader } from "@/components/ui";
import { api } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { errorMessage, formatDateTime } from "@/lib/format";

export default function ProjectsPage() {
  const { can } = useMe();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.listProjects });
  const connections = useQuery({ queryKey: ["connections"], queryFn: api.listConnections });
  const connName = (id: number) => connections.data?.find((c) => c.id === id)?.name ?? `#${id}`;

  return (
    <>
      <PageHeader
        title="Projects"
        subtitle="A project pairs a source and a target database with compare options."
        actions={
          can("project.write") && (
            <LinkButton href="/projects/new" variant="primary">
              New project
            </LinkButton>
          )
        }
      />
      {projects.isPending && <Loading />}
      {projects.error && <Alert>{errorMessage(projects.error)}</Alert>}
      {projects.data?.length === 0 && (
        <Card>
          <Empty>
            {can("project.write") ? (
              <>
                No projects yet. Add <Link className="text-blue-600 underline" href="/connections">connections</Link>,
                then create a project.
              </>
            ) : (
              "No projects yet."
            )}
          </Empty>
        </Card>
      )}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {projects.data?.map((p) => (
          <Link key={p.id} href={`/projects/${p.id}`}>
            <Card className="h-full p-4 transition-colors hover:border-blue-400">
              <div className="font-semibold text-gray-900">{p.name}</div>
              <div className="mt-2 flex items-center gap-2 text-sm text-gray-600">
                <span className="truncate rounded bg-gray-100 px-1.5 py-0.5">{connName(p.source_connection_id)}</span>
                <span>→</span>
                <span className="truncate rounded bg-gray-100 px-1.5 py-0.5">{connName(p.target_connection_id)}</span>
              </div>
              <div className="mt-3 text-xs text-gray-500">Updated {formatDateTime(p.updated_at)}</div>
            </Card>
          </Link>
        ))}
      </div>
    </>
  );
}
