"use client";

import { useQuery } from "@tanstack/react-query";
import { useCallback } from "react";
import { api, type Permission } from "./api";

export const meQueryKey = ["me"] as const;

export const roleLabels = {
  admin: "Admin",
  operator: "Operator",
  viewer: "Viewer",
} as const;

/**
 * Returns the signed-in user and a permission check. The server enforces
 * every permission; the check only decides which controls to show.
 */
export function useMe() {
  const me = useQuery({ queryKey: meQueryKey, queryFn: api.me, staleTime: 5 * 60_000 });
  const permissions = me.data?.permissions;
  const can = useCallback((p: Permission) => permissions?.includes(p) ?? false, [permissions]);
  return { me: me.data, can };
}

/** Returns a same-origin path to return to after signing in, or null. */
export function safeNextPath(next: string | null): string | null {
  // Browsers treat a backslash like "/", so "/\host" would leave the origin.
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return null;
  if (next.startsWith("/login")) return null;
  return next;
}

/** Sends the browser to the sign-in page, remembering the current page. */
export function redirectToLogin() {
  if (window.location.pathname.startsWith("/login")) return;
  const next = window.location.pathname + window.location.search;
  window.location.assign(`/login?next=${encodeURIComponent(next)}`);
}
