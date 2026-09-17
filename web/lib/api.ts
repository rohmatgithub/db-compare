export type Role = "admin" | "operator" | "viewer";

export type Permission =
  | "run.export"
  | "run.execute"
  | "run.delete"
  | "project.write"
  | "project.delete"
  | "connection.manage"
  | "connection.protected"
  | "user.manage";

export type Me = {
  id: number;
  user: string;
  role: Role;
  permissions: Permission[];
};

export type User = {
  id: number;
  username: string;
  role: Role;
  disabled: boolean;
  created_at: string;
  updated_at: string;
  last_login_at: string | null;
};

export type Engine = "mysql" | "postgres";
export type SSLMode = "disable" | "prefer" | "require" | "verify-full";

export type Connection = {
  id: number;
  name: string;
  engine: Engine;
  host: string;
  port: number;
  database: string;
  schema: string;
  username: string;
  has_password: boolean;
  ssl_mode: SSLMode;
  server_version: string;
  protected: boolean;
  created_at: string;
  updated_at: string;
};

export type ConnectionInput = {
  name: string;
  engine: Engine;
  host: string;
  port: number;
  database: string;
  schema: string;
  username: string;
  // null keeps the stored password when updating.
  password: string | null;
  ssl_mode: SSLMode;
  protected: boolean;
};

export type TestConnectionResult = {
  ok: boolean;
  version?: string;
  tables: number;
  warning?: string;
  error?: string;
};

export type Options = {
  include_tables: string[] | null;
  exclude_tables: string[] | null;
  ignore_columns: string[] | null;
  mask_columns: string[] | null;
  key_overrides: Record<string, string[]> | null;
  check_column_order: boolean;
  skip_views: boolean;
  skip_routines: boolean;
  include_row_diff: boolean;
  row_diff_limit: number;
  chunk_size: number;
  parallelism: number;
};

export type Project = {
  id: number;
  name: string;
  source_connection_id: number;
  target_connection_id: number;
  options: Options;
  created_at: string;
  updated_at: string;
};

export type ProjectInput = Pick<Project, "name" | "source_connection_id" | "target_connection_id" | "options">;

export type RunStatus = "pending" | "running" | "completed" | "cancelled" | "interrupted" | "failed";

export type RunProgress = {
  phase: string;
  schema_done: boolean;
  tables_total: number;
  tables_done: number;
  active_tables: string[] | null;
};

export type Counts = {
  identical: number;
  different: number;
  only_source: number;
  only_target: number;
};

export type RunSummary = {
  schema: Record<string, number>;
  data: Record<string, number>;
  rowdiff: Record<string, number>;
  rows: Counts;
};

export type Run = {
  id: number;
  project_id: number;
  status: RunStatus;
  options: Options;
  progress: RunProgress;
  source_label: string;
  target_label: string;
  error: string;
  created_by: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  heartbeat_at: string | null;
};

export type RunDetail = Run & { active: boolean; summary: RunSummary; protected: boolean };

export type ObjectStatus = "same" | "changed" | "only_source" | "only_target";

export type SchemaChange = {
  kind: string;
  name: string;
  status: ObjectStatus;
  source?: string;
  target?: string;
};

export type SchemaItem = {
  id: number;
  object_type: string;
  name: string;
  status: ObjectStatus;
  changes: SchemaChange[] | null;
  source_ddl?: string;
  target_ddl?: string;
};

export type DataStatus = "pending" | "identical" | "different" | "error";
export type RowDiffStatus =
  | "not_run"
  | "not_needed"
  | "needs_key"
  | "running"
  | "done"
  | "cancelled"
  | "interrupted"
  | "error";

export type TableResult = Counts & {
  run_id: number;
  table_name: string;
  key_columns: string[];
  columns: string[];
  notes: string[];
  source_rows: number | null;
  target_rows: number | null;
  data_status: DataStatus;
  rowdiff_status: RowDiffStatus;
  truncated: boolean;
  error: string;
  duration_ms: number;
  updated_at: string;
};

export type Category = "different" | "only_source" | "only_target";

export type RowDiff = {
  id: number;
  category: Category;
  key: string[];
  source: (string | null)[] | null;
  target: (string | null)[] | null;
  diff_columns: string[];
};

export type RowsPage = { rows: RowDiff[]; next_after: number };

export type RowDiffProgress = Counts & {
  table: string;
  chunks_done: number;
  chunks_total: number;
  rows_scanned: number;
};

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export async function errorFrom(res: Response): Promise<ApiError> {
  let message = `${res.status} ${res.statusText}`;
  try {
    const body = await res.json();
    if (body?.error) message = body.error;
  } catch {
    // The body is not JSON; the status line is the best message available.
  }
  return new ApiError(message, res.status);
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  if (!res.ok) throw await errorFrom(res);
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const tablePath = (runId: number | string, table: string) =>
  `/api/runs/${runId}/tables/${encodeURIComponent(table)}`;

export const api = {
  me: () => request<Me>("GET", "/api/me"),
  login: (username: string, password: string) => request<Me>("POST", "/api/auth/login", { username, password }),
  logout: () => request<void>("POST", "/api/auth/logout"),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>("PUT", "/api/me/password", { current_password: currentPassword, new_password: newPassword }),

  listUsers: () => request<User[]>("GET", "/api/users"),
  createUser: (input: { username: string; password: string; role: Role }) =>
    request<User>("POST", "/api/users", input),
  updateUser: (id: number, input: { role: Role; disabled: boolean }) => request<User>("PUT", `/api/users/${id}`, input),
  resetUserPassword: (id: number, password: string) =>
    request<void>("PUT", `/api/users/${id}/password`, { password }),
  deleteUser: (id: number) => request<void>("DELETE", `/api/users/${id}`),

  listConnections: () => request<Connection[]>("GET", "/api/connections"),
  createConnection: (input: ConnectionInput) => request<Connection>("POST", "/api/connections", input),
  updateConnection: (id: number, input: ConnectionInput) =>
    request<Connection>("PUT", `/api/connections/${id}`, input),
  deleteConnection: (id: number) => request<void>("DELETE", `/api/connections/${id}`),
  testConnection: (input: ConnectionInput & { id?: number }) =>
    request<TestConnectionResult>("POST", "/api/connections/test", input),

  listProjects: () => request<Project[]>("GET", "/api/projects"),
  getProject: (id: number | string) => request<Project>("GET", `/api/projects/${id}`),
  createProject: (input: ProjectInput) => request<Project>("POST", "/api/projects", input),
  updateProject: (id: number, input: ProjectInput) => request<Project>("PUT", `/api/projects/${id}`, input),
  deleteProject: (id: number) => request<void>("DELETE", `/api/projects/${id}`),
  listRuns: (projectId: number | string) => request<Run[]>("GET", `/api/projects/${projectId}/runs`),
  createRun: (projectId: number) => request<Run>("POST", `/api/projects/${projectId}/runs`),

  getRun: (id: number | string) => request<RunDetail>("GET", `/api/runs/${id}`),
  deleteRun: (id: number) => request<void>("DELETE", `/api/runs/${id}`),
  cancelRun: (id: number | string) => request<{ cancelled: boolean }>("POST", `/api/runs/${id}/cancel`),
  listSchemaItems: (runId: number | string) => request<SchemaItem[]>("GET", `/api/runs/${runId}/schema`),
  getSchemaItem: (runId: number | string, itemId: number) =>
    request<SchemaItem>("GET", `/api/runs/${runId}/schema/${itemId}`),
  listTables: (runId: number | string) => request<TableResult[]>("GET", `/api/runs/${runId}/tables`),
  getTable: (runId: number | string, table: string) => request<TableResult>("GET", tablePath(runId, table)),
  listRows: (runId: number | string, table: string, category: Category, after: number) =>
    request<RowsPage>(
      "GET",
      `${tablePath(runId, table)}/rows?category=${category}&after=${after}&limit=100`,
    ),
};
