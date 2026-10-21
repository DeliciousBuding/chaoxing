export type JsonObject = Record<string, any>;

export type Role = "owner" | "admin" | "operator" | "viewer";

export interface PlatformUser {
  id: number;
  username: string;
  display_name: string;
  role: Role;
  status: "active" | "disabled";
  created_at?: string;
  updated_at?: string;
  last_login_at?: string;
  accounts?: string[];
}

export interface Capabilities {
  manage_users: boolean;
  view_audit: boolean;
  manage_accounts: boolean;
  sensitive_read: boolean;
  tool_scopes: string[];
}

export interface SessionInfo {
  authenticated: boolean;
  account?: string;
  csrf: string;
  version: string;
  expires_at?: string;
  user?: Pick<PlatformUser, "id" | "username" | "display_name" | "role">;
  capabilities?: Capabilities;
}

export interface AccountInfo {
  current: string;
  accounts: Array<{ id: string; name: string }>;
}

export interface LocationPreset {
  id?: number;
  label: string;
  address?: string;
  latitude: number;
  longitude: number;
  coordinate_system?: string;
  created_at?: string;
  updated_at?: string;
}

export interface LocationBinding {
  course_id: string;
  class_id: string;
  schedule_key?: string;
  preset_id: number;
  updated_at?: string;
}

export interface SignAudit {
  account: string;
  course_id: string;
  class_id: string;
  activity_id: string;
  method: string;
  state: string;
  code?: string;
  verified: boolean;
  egress_ip?: string;
  egress_ip_source?: string;
  address?: string;
  latitude?: number;
  longitude?: number;
  created_at: string;
}

export interface AdminAccount {
  id: string;
  name: string;
  watch_enabled: boolean;
  auto_sign: boolean;
  auto_answer: boolean;
  notify_only: boolean;
  location_enabled: boolean;
  classes: number;
  explicit_members: number;
}

export interface AuditEvent {
  id: number;
  created_at: string;
  actor_id: number;
  actor_username: string;
  actor_role: string;
  account_key?: string;
  category: string;
  action: string;
  target?: string;
  success: boolean;
  ip?: string;
  user_agent?: string;
  request_id?: string;
  details?: Record<string, any>;
}

export interface MapConfig {
  enabled: boolean;
  ak?: string;
  center_latitude?: number;
  center_longitude?: number;
  zoom?: number;
}

export interface WatchSchedule {
  course_id: string;
  class_id: string;
  key: string;
  days: string[];
  start: string;
  end: string;
  weeks?: string;
  room?: string;
}

export interface ToolDefinition {
  id: string;
  name: string;
  title: string;
  description: string;
  scope: string;
  fields: Record<string, {
    type: string;
    description: string;
    required: boolean;
    min?: number;
    max?: number;
    enum?: string[];
    max_length?: number;
  }>;
}

export class ApiError extends Error {
  code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

function csrfToken(): string {
  const prefix = "__Host-chaoxing_csrf=";
  for (const part of document.cookie.split(";")) {
    const item = part.trim();
    if (item.startsWith(prefix)) return decodeURIComponent(item.slice(prefix.length));
  }
  return "";
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body) headers.set("Content-Type", "application/json");
  const method = init.method ?? "GET";
  if (method !== "GET") headers.set("X-Chaoxing-CSRF", csrfToken());
  const response = await fetch(path, { ...init, method, headers, credentials: "same-origin" });
  let body: any = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  if (!response.ok || body?.ok === false) {
    const error = body?.error ?? {};
    throw new ApiError(error.code ?? `HTTP_${response.status}`, error.message ?? response.statusText);
  }
  return body as T;
}

export const api = {
  session: () => request<SessionInfo>("/api/auth/session"),
  login: (username: string, password: string) => request<{ authenticated: boolean }>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password })
  }),
  logout: () => request<{ authenticated: boolean }>("/api/auth/logout", { method: "POST" }),
  account: () => request<AccountInfo>("/api/account"),
  selectAccount: (account: string) => request<{ current: string }>("/api/account/select", {
    method: "POST",
    body: JSON.stringify({ account })
  }),
  status: () => request<JsonObject>("/api/status"),
  captures: (limit = 100, options: { includePage?: boolean; account?: string; activityKey?: string } = {}) => {
    const query = new URLSearchParams({ limit: String(limit) });
    if (options.includePage) query.set("include_page", "true");
    if (options.account) query.set("account", options.account);
    if (options.activityKey) query.set("activity_key", options.activityKey);
    return request<{ items: JsonObject[] }>(`/api/captures?${query.toString()}`);
  },
  signAudits: (limit = 20) => request<{ items: SignAudit[] }>(`/api/sign-audits?limit=${limit}`),
  locations: () => request<{ items: LocationPreset[] }>("/api/locations"),
  saveLocation: (preset: LocationPreset) => request<LocationPreset>(preset.id ? `/api/locations/${preset.id}` : "/api/locations", {
    method: preset.id ? "PUT" : "POST",
    body: JSON.stringify(preset)
  }),
  deleteLocation: (id: number) => request<{ deleted: boolean }>(`/api/locations/${id}`, { method: "DELETE" }),
  locationBindings: () => request<{ items: LocationBinding[] }>("/api/location-bindings"),
  watchSchedules: () => request<{ items: WatchSchedule[]; timezone: string; coordinate_system: string }>("/api/watch/schedules"),
  saveLocationBinding: (binding: LocationBinding) => request<{ saved: boolean }>("/api/location-bindings", {
    method: "POST",
    body: JSON.stringify(binding)
  }),
  deleteLocationBinding: (binding: LocationBinding) => request<{ deleted: boolean }>("/api/location-bindings", {
    method: "DELETE",
    body: JSON.stringify(binding)
  }),
  mapConfig: () => request<MapConfig>("/api/map/config"),
  geocode: (address: string) => request<LocationPreset>("/api/map/geocode", {
    method: "POST",
    body: JSON.stringify({ address })
  }),
  reverseGeocode: (latitude: number, longitude: number, coordinateSystem = "bd09") => request<{ address: string }>("/api/map/reverse-geocode", {
    method: "POST",
    body: JSON.stringify({ latitude, longitude, coordinate_system: coordinateSystem })
  }),
  tools: () => request<{ items: ToolDefinition[] }>("/api/tools"),
  call: (tool: string, args: JsonObject = {}) => request<{ ok: boolean; data: JsonObject }>("/api/tools/call", {
    method: "POST",
    body: JSON.stringify({ tool, arguments: args })
  }),
  members: () => request<{ items: PlatformUser[] }>("/api/admin/users"),
  createMember: (input: {
    username: string;
    display_name: string;
    password: string;
    role: Role;
    accounts: string[];
  }) => request<PlatformUser>("/api/admin/users", {
    method: "POST",
    body: JSON.stringify(input)
  }),
  updateMember: (id: number, input: Record<string, unknown>) => request<PlatformUser>(`/api/admin/users/${id}`, {
    method: "PATCH",
    body: JSON.stringify(input)
  }),
  adminAccounts: () => request<{ items: AdminAccount[]; issuer: string; version: string }>("/api/admin/accounts"),
  audit: (options: { limit?: number; offset?: number; category?: string; account?: string; actor?: string } = {}) => {
    const query = new URLSearchParams({
      limit: String(options.limit ?? 50),
      offset: String(options.offset ?? 0)
    });
    if (options.category) query.set("category", options.category);
    if (options.account) query.set("account", options.account);
    if (options.actor) query.set("actor", options.actor);
    return request<{ items: AuditEvent[]; total: number; limit: number; offset: number }>(`/api/admin/audit?${query.toString()}`);
  }
};
