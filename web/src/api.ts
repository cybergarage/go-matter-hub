// The hub's HTTP API (/api/v1). Requests are same-origin, so the browser
// sends the session cookie by itself.

export interface Health {
  status: string;
  version: string;
}

export interface Session {
  authRequired: boolean;
  authenticated: boolean;
}

export interface Hub {
  version: string;
  authRequired: boolean;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    // A response without a JSON body is reported by its status below.
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } } | null)?.error;
    throw new ApiError(res.status, err?.code ?? 'error', err?.message ?? `HTTP ${res.status}`);
  }
  return data as T;
}

export const getHealth = () => request<Health>('GET', '/health');
export const getSession = () => request<Session>('GET', '/session');
export const signIn = (token: string) => request<Session>('POST', '/session', { token });
export const signOut = () => request<Session>('DELETE', '/session');
export const getHub = () => request<Hub>('GET', '/hub');
