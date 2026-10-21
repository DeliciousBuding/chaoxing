export type JsonObject = Record<string, any>;

export interface SessionInfo {
  authenticated: boolean;
  account?: string;
  csrf: string;
  version: string;
  expires_at?: string;
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
  login: (password: string) => request<{ authenticated: boolean }>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ password })
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
  })
};
