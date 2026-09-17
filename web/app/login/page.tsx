"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState, type FormEvent } from "react";
import { Alert, Button, Card, Field, Input, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { meQueryKey, safeNextPath } from "@/lib/auth";
import { errorMessage } from "@/lib/format";

function LoginForm() {
  const router = useRouter();
  const qc = useQueryClient();
  const next = safeNextPath(useSearchParams().get("next")) ?? "/projects";
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: () => api.login(username, password),
    onSuccess: (me) => {
      // Data cached for a previous user must not leak into this session.
      qc.clear();
      qc.setQueryData(meQueryKey, me);
      router.replace(next);
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field label="Username">
        <Input
          required
          autoFocus
          autoComplete="username"
          autoCapitalize="none"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </Field>
      <Field label="Password">
        <Input
          required
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      {login.error && <Alert>{errorMessage(login.error)}</Alert>}
      <Button type="submit" variant="primary" className="w-full" disabled={login.isPending}>
        {login.isPending && <Spinner />} Sign in
      </Button>
    </form>
  );
}

export default function LoginPage() {
  return (
    <div className="mx-auto mt-16 max-w-sm">
      <div className="mb-6 flex items-center justify-center gap-2 text-lg font-semibold text-gray-900">
        <span className="grid h-8 w-8 place-items-center rounded-md bg-blue-600 text-xs font-bold text-white">DB</span>
        DB Compare
      </div>
      <Card className="p-6">
        <h1 className="mb-4 text-base font-semibold text-gray-900">Sign in</h1>
        <Suspense>
          <LoginForm />
        </Suspense>
      </Card>
      <p className="mt-4 text-center text-xs text-gray-500">Ask an admin if you need an account or a password reset.</p>
    </div>
  );
}
