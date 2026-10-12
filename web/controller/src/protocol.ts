// Minimal client of the public contract (api/asyncapi.yaml, api/openapi.yaml).
// It does not import anything from the relay implementation.

export const PROTOCOL_VERSION = "1.0";

export type ErrorCode = string;
export interface ProtoError { code: ErrorCode; message: string; retryable: boolean; retryAfterMs?: number }
export interface Envelope<P = any> { protocolVersion: string; type: string; messageId: string; sessionId?: string; correlationId?: string; payload: P }
export interface ActionDescriptor { id: string; title: string; description?: string; capability: string; parameters: any; confirmation: string }
export interface Catalog { revision: number; actions: ActionDescriptor[] }
export interface Participant { participantId: string; displayName: string; state: "pending" | "enabled" | "disabled" | "revoked"; connected: boolean; capabilities: string[]; joinedAt: string }
export interface SessionView {
  sessionId: string; status: "open" | "closed"; paused: boolean; requireApproval: boolean; authzRevision: number;
  host: { online: boolean; ready: boolean; readinessReason?: string; target?: { label: string; focusCheck: string }; epoch?: number };
  catalog: Catalog | null; expiresAt: string; you?: Participant;
}

export class HttpError extends Error {
  constructor(public status: number, public error: ProtoError) { super(error.message); }
}

export async function join(sessionId: string, displayName: string, password: string) {
  const res = await fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}/join`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ displayName, password }),
    credentials: "omit", cache: "no-store",
  });
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new HttpError(res.status, body?.error ?? { code: "INTERNAL_ERROR", message: "HTTP " + res.status, retryable: true });
  return body as { participantId: string; token: string; state: Participant["state"]; expiresAt: string; wsUrl: string };
}

export function leave(sessionId: string, token: string) {
  // keepalive lets this run during pagehide; failure is harmless (token expires).
  fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}/leave`, { method: "POST", headers: { Authorization: "Bearer " + token }, keepalive: true }).catch(() => {});
}

export function uuid(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // randomUUID needs a secure context; plain-http tunnels (bore.pub) do not have one.
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export function envelope(type: string, sessionId: string, payload: unknown): Envelope {
  return { protocolVersion: PROTOCOL_VERSION, type, messageId: uuid(), sessionId, payload };
}

export function wsURL(): string {
  return (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/api/v1/ws";
}
