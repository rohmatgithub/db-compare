"use client";

import { useMutation } from "@tanstack/react-query";
import Link from "next/link";
import { usePathname } from "next/navigation";
import type { Permission } from "@/lib/api";
import { api } from "@/lib/api";
import { roleLabels, useMe } from "@/lib/auth";
import { cx } from "./ui";

const links: { href: string; label: string; match: string[]; permission?: Permission }[] = [
  { href: "/projects", label: "Projects", match: ["/projects", "/runs"] },
  { href: "/connections", label: "Connections", match: ["/connections"] },
  { href: "/users", label: "Users", match: ["/users"], permission: "user.manage" },
];

export function Nav() {
  const pathname = usePathname();
  const { me, can } = useMe();
  const logout = useMutation({
    mutationFn: api.logout,
    // A full page load drops every cached query of the signed-out user.
    onSettled: () => window.location.assign("/login"),
  });

  return (
    <header className="border-b border-gray-200 bg-white">
      <div className="mx-auto flex h-14 max-w-[1400px] items-center gap-6 px-4 sm:px-6">
        <Link href="/projects" className="flex items-center gap-2 font-semibold text-gray-900">
          <span className="grid h-7 w-7 place-items-center rounded-md bg-blue-600 text-xs font-bold text-white">DB</span>
          <span className="hidden sm:inline">DB Compare</span>
        </Link>
        <nav className="flex gap-1">
          {links
            .filter((l) => !l.permission || can(l.permission))
            .map((l) => {
              const active = l.match.some((m) => pathname.startsWith(m));
              return (
                <Link
                  key={l.href}
                  href={l.href}
                  className={cx(
                    "rounded-md px-3 py-1.5 text-sm font-medium",
                    active ? "bg-gray-100 text-gray-900" : "text-gray-600 hover:text-gray-900",
                  )}
                >
                  {l.label}
                </Link>
              );
            })}
        </nav>
        {me && (
          <div className="ml-auto flex items-center gap-3 text-sm">
            <Link
              href="/account"
              className={cx(
                "flex items-center gap-2 rounded-md px-2 py-1 hover:bg-gray-100",
                pathname.startsWith("/account") && "bg-gray-100",
              )}
            >
              <span className="text-gray-700">{me.user}</span>
              <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs font-medium text-gray-600">
                {roleLabels[me.role]}
              </span>
            </Link>
            <button
              type="button"
              className="text-gray-500 hover:text-gray-900 disabled:text-gray-300"
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
            >
              Sign out
            </button>
          </div>
        )}
      </div>
    </header>
  );
}
