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
  /** The hub's fabric, 16 hexadecimal digits. */
  fabricId?: string;
  nodes?: { total: number; online: number };
}

export type NodeStatus = 'connecting' | 'online' | 'offline';

export interface Attribute {
  endpoint: number;
  cluster: number;
  attribute: number;
  /**
   * The value the node reported. An integer beyond 2^53 is a string of
   * decimal digits, an octet string a string of hexadecimal digits, and a
   * structure an object keyed by field number.
   */
  value: unknown;
  /** The Interaction Model status reported in place of a value. */
  status?: number;
  updatedAt: string;
}

export interface MatterNode {
  /** 16 hexadecimal digits. */
  id: string;
  vendorId: number;
  productId: number;
  commissionedAt: string;
  status: NodeStatus;
  error?: string;
  lastReportAt: string;
  attributes: Attribute[];
}

export interface StatusEvent {
  node: string;
  status: NodeStatus;
  error?: string;
}

export interface AttributesEvent {
  node: string;
  /** True when the attributes are the whole state of the node. */
  priming: boolean;
  attributes: Attribute[];
}

export interface EventHandlers {
  nodes(nodes: MatterNode[]): void;
  status(event: StatusEvent): void;
  attributes(event: AttributesEvent): void;
  /** The stream is connected (true) or is trying to connect again (false). */
  connection(open: boolean): void;
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
export const getNodes = () => request<{ nodes: MatterNode[] }>('GET', '/nodes');

/**
 * Opens the stream of node states. The browser connects again by itself when
 * the stream breaks; every connection starts with all the nodes. The returned
 * function closes the stream.
 */
export function openEvents(handlers: EventHandlers): () => void {
  const source = new EventSource('/api/v1/events');
  const on = <T>(name: string, handle: (data: T) => void) => {
    source.addEventListener(name, (event) => {
      handle(JSON.parse((event as MessageEvent<string>).data) as T);
    });
  };
  on<{ nodes: MatterNode[] }>('nodes', (data) => handlers.nodes(data.nodes));
  on<StatusEvent>('status', (data) => handlers.status(data));
  on<AttributesEvent>('attributes', (data) => handlers.attributes(data));
  source.onopen = () => handlers.connection(true);
  source.onerror = () => handlers.connection(false);
  return () => source.close();
}
