"use client";

import { useQuery } from "@tanstack/react-query";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { api, ApiError } from "@/lib/api";
import { meQueryKey } from "@/lib/auth";
import { errorMessage } from "@/lib/format";
import { Nav } from "./nav";
import { Alert, Loading } from "./ui";

const mainClass = "mx-auto max-w-[1400px] px-4 py-6 sm:px-6";

/** Renders the sign-in page bare and every other page behind a session. */
export function AppShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  if (pathname.startsWith("/login")) return <main className={mainClass}>{children}</main>;
  return <SignedIn>{children}</SignedIn>;
}

function SignedIn({ children }: { children: ReactNode }) {
  const me = useQuery({ queryKey: meQueryKey, queryFn: api.me, staleTime: 5 * 60_000 });
  let content = children;
  if (me.isPending) content = <Loading />;
  else if (me.error instanceof ApiError && me.error.status === 401) content = <Loading label="Redirecting to sign in…" />;
  else if (me.error) content = <Alert>{errorMessage(me.error)}</Alert>;
  return (
    <>
      {me.data && <Nav />}
      <main className={mainClass}>{content}</main>
    </>
  );
}
