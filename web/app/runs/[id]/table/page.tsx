"use client";

import { useParams, useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { TableDetail } from "@/components/run/table-detail";
import { Alert, Loading } from "@/components/ui";

// The table name travels in the query string because it may contain
// characters that are awkward in a path segment.
function TablePageContent() {
  const { id } = useParams<{ id: string }>();
  const name = useSearchParams().get("name");
  if (!name) return <Alert>No table selected.</Alert>;
  return <TableDetail runId={id} name={name} />;
}

export default function TablePage() {
  return (
    <Suspense fallback={<Loading />}>
      <TablePageContent />
    </Suspense>
  );
}
