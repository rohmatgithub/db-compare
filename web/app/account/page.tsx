"use client";

import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Alert, Button, Card, Field, Input, PageHeader, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { roleLabels, useMe } from "@/lib/auth";
import { errorMessage } from "@/lib/format";

export default function AccountPage() {
  const { me } = useMe();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const mismatch = confirm !== "" && next !== confirm;

  const change = useMutation({
    mutationFn: () => api.changePassword(current, next),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setConfirm("");
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (next === confirm) change.mutate();
  };

  return (
    <>
      <PageHeader
        title="Account"
        subtitle={me && `Signed in as ${me.user} · ${roleLabels[me.role]}`}
      />
      <Card className="max-w-md p-5">
        <h2 className="mb-4 font-semibold text-gray-900">Change password</h2>
        <form onSubmit={submit} className="space-y-3">
          <Field label="Current password">
            <Input
              required
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </Field>
          <Field label="New password" hint="8 to 72 characters. Other sessions of this account are signed out.">
            <Input
              required
              type="password"
              minLength={8}
              maxLength={72}
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </Field>
          <Field label="Confirm new password">
            <Input
              required
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          {mismatch && <Alert tone="warning">The new passwords do not match.</Alert>}
          {change.error && <Alert>{errorMessage(change.error)}</Alert>}
          {change.isSuccess && <Alert tone="success">Password changed.</Alert>}
          <div className="flex justify-end">
            <Button type="submit" variant="primary" disabled={change.isPending || mismatch}>
              {change.isPending && <Spinner />} Change password
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
