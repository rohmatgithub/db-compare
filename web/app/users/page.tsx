"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Alert, Button, Card, Empty, Field, Input, Loading, PageHeader, Select, Spinner } from "@/components/ui";
import { api, type Role, type User } from "@/lib/api";
import { roleLabels, useMe } from "@/lib/auth";
import { errorMessage, formatDateTime } from "@/lib/format";

const roles: Role[] = ["viewer", "operator", "admin"];

const roleHelp: Record<Role, string> = {
  viewer: "View projects, runs, and results; export Excel.",
  operator: "Viewer rights, plus run compares and create or edit projects.",
  admin: "Everything, including connections, deleting, users, and protected connections.",
};

export default function UsersPage() {
  const qc = useQueryClient();
  const { me, can } = useMe();
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers, enabled: can("user.manage") });
  const [panel, setPanel] = useState<{ kind: "new" } | { kind: "password"; user: User } | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["users"] });

  const update = useMutation({
    mutationFn: ({ user, role, disabled }: { user: User; role: Role; disabled: boolean }) =>
      api.updateUser(user.id, { role, disabled }),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.deleteUser(id),
    onSuccess: refresh,
  });

  if (me && !can("user.manage")) return <Alert>Only admins can manage users.</Alert>;

  return (
    <>
      <PageHeader
        title="Users"
        subtitle="Accounts that can sign in, and what their role allows."
        actions={
          <Button variant="primary" onClick={() => setPanel({ kind: "new" })}>
            Add user
          </Button>
        }
      />

      <div className="mb-4 grid gap-3 md:grid-cols-3">
        {roles.map((r) => (
          <div key={r} className="rounded-md border border-gray-200 bg-white px-3 py-2 text-sm">
            <div className="font-medium text-gray-900">{roleLabels[r]}</div>
            <div className="text-gray-500">{roleHelp[r]}</div>
          </div>
        ))}
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_380px]">
        <Card>
          {users.isPending && <Loading />}
          {users.error && <Alert>{errorMessage(users.error)}</Alert>}
          {(update.error || remove.error) && (
            <div className="p-3">
              <Alert>{errorMessage(update.error ?? remove.error)}</Alert>
            </div>
          )}
          {users.data?.length === 0 && <Empty>No users.</Empty>}
          {users.data && users.data.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-gray-200 bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
                  <tr>
                    <th className="px-4 py-2 font-medium">Username</th>
                    <th className="px-4 py-2 font-medium">Role</th>
                    <th className="px-4 py-2 font-medium">Status</th>
                    <th className="px-4 py-2 font-medium">Last sign-in</th>
                    <th className="px-4 py-2" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {users.data.map((u) => {
                    const self = u.id === me?.id;
                    const busy = update.isPending || remove.isPending;
                    return (
                      <tr key={u.id} className={u.disabled ? "text-gray-400" : ""}>
                        <td className="px-4 py-2 font-medium">
                          {u.username}
                          {self && <span className="ml-2 text-xs font-normal text-gray-500">(you)</span>}
                        </td>
                        <td className="px-4 py-2">
                          {self ? (
                            roleLabels[u.role]
                          ) : (
                            <Select
                              className="w-32"
                              value={u.role}
                              disabled={busy}
                              onChange={(e) => update.mutate({ user: u, role: e.target.value as Role, disabled: u.disabled })}
                            >
                              {roles.map((r) => (
                                <option key={r} value={r}>
                                  {roleLabels[r]}
                                </option>
                              ))}
                            </Select>
                          )}
                        </td>
                        <td className="px-4 py-2">{u.disabled ? "Disabled" : "Active"}</td>
                        <td className="px-4 py-2 text-gray-500">{formatDateTime(u.last_login_at)}</td>
                        <td className="whitespace-nowrap px-4 py-2 text-right">
                          {!self && (
                            <>
                              <Button variant="ghost" disabled={busy} onClick={() => setPanel({ kind: "password", user: u })}>
                                Reset password
                              </Button>
                              <Button
                                variant="ghost"
                                disabled={busy}
                                onClick={() => update.mutate({ user: u, role: u.role, disabled: !u.disabled })}
                              >
                                {u.disabled ? "Enable" : "Disable"}
                              </Button>
                              <Button
                                variant="ghost"
                                className="text-red-600"
                                disabled={busy}
                                onClick={() => {
                                  if (window.confirm(`Delete user "${u.username}"?`)) remove.mutate(u.id);
                                }}
                              >
                                Delete
                              </Button>
                            </>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        {panel && (
          <Card className="p-4">
            {panel.kind === "new" ? (
              <NewUserForm
                onCancel={() => setPanel(null)}
                onSaved={() => {
                  refresh();
                  setPanel(null);
                }}
              />
            ) : (
              <ResetPasswordForm key={panel.user.id} user={panel.user} onDone={() => setPanel(null)} />
            )}
          </Card>
        )}
      </div>
    </>
  );
}

function NewUserForm({ onSaved, onCancel }: { onSaved: () => void; onCancel: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const create = useMutation({
    mutationFn: () => api.createUser({ username, password, role }),
    onSuccess: onSaved,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  return (
    <form onSubmit={submit} className="space-y-3">
      <h2 className="font-semibold text-gray-900">Add user</h2>
      <Field label="Username" hint="3–64 characters: lowercase letters, digits, “.”, “_” or “-”.">
        <Input
          required
          autoComplete="off"
          autoCapitalize="none"
          value={username}
          onChange={(e) => setUsername(e.target.value.toLowerCase())}
        />
      </Field>
      <Field label="Initial password" hint="8 to 72 characters. Share it privately; the user can change it under Account.">
        <Input
          required
          type="password"
          minLength={8}
          maxLength={72}
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      <Field label="Role" hint={roleHelp[role]}>
        <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {roles.map((r) => (
            <option key={r} value={r}>
              {roleLabels[r]}
            </option>
          ))}
        </Select>
      </Field>
      {create.error && <Alert>{errorMessage(create.error)}</Alert>}
      <div className="flex justify-end gap-2 pt-2">
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" disabled={create.isPending}>
          {create.isPending && <Spinner />} Add user
        </Button>
      </div>
    </form>
  );
}

function ResetPasswordForm({ user, onDone }: { user: User; onDone: () => void }) {
  const [password, setPassword] = useState("");
  const reset = useMutation({
    mutationFn: () => api.resetUserPassword(user.id, password),
    onSuccess: () => setPassword(""),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    reset.mutate();
  };
  return (
    <form onSubmit={submit} className="space-y-3">
      <h2 className="font-semibold text-gray-900">Reset password for {user.username}</h2>
      <Field label="New password" hint="8 to 72 characters. The user is signed out everywhere.">
        <Input
          required
          type="password"
          minLength={8}
          maxLength={72}
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      {reset.error && <Alert>{errorMessage(reset.error)}</Alert>}
      {reset.isSuccess && <Alert tone="success">Password reset.</Alert>}
      <div className="flex justify-end gap-2 pt-2">
        <Button variant="ghost" onClick={onDone}>
          {reset.isSuccess ? "Close" : "Cancel"}
        </Button>
        <Button type="submit" variant="primary" disabled={reset.isPending}>
          {reset.isPending && <Spinner />} Reset password
        </Button>
      </div>
    </form>
  );
}
