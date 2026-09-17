"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { ProjectForm } from "@/components/project-form";
import { Alert, Card, PageHeader } from "@/components/ui";
import { useMe } from "@/lib/auth";

export default function NewProjectPage() {
  const router = useRouter();
  const qc = useQueryClient();
  const { can } = useMe();
  if (!can("project.write")) return <Alert>Your role cannot create projects.</Alert>;
  return (
    <>
      <PageHeader title="New project" />
      <Card className="p-5">
        <ProjectForm
          project={null}
          onCancel={() => router.push("/projects")}
          onSaved={(p) => {
            qc.invalidateQueries({ queryKey: ["projects"] });
            router.push(`/projects/${p.id}`);
          }}
        />
      </Card>
    </>
  );
}
