// Limits set by the server operator, sent with `ready`. Older servers send
// none, so the defaults are the values those servers enforce. The server
// checks everything itself; these only make the app's checks and messages fit.
export interface ServerLimits {
  messageChars: number;
  uploadBytes: number;
  spaceRoles: number;
  /** "admins": only server admins may create invites. */
  invites: "members" | "admins";
}

export const DEFAULT_LIMITS: ServerLimits = {
  messageChars: 4000,
  uploadBytes: 200 * 1024 * 1024,
  spaceRoles: 25,
  invites: "members",
};

export function readLimits(raw?: Partial<ServerLimits>): ServerLimits {
  const num = (v: unknown, def: number) => (typeof v === "number" && v > 0 ? v : def);
  return {
    messageChars: num(raw?.messageChars, DEFAULT_LIMITS.messageChars),
    uploadBytes: num(raw?.uploadBytes, DEFAULT_LIMITS.uploadBytes),
    spaceRoles: num(raw?.spaceRoles, DEFAULT_LIMITS.spaceRoles),
    invites: raw?.invites === "admins" ? "admins" : "members",
  };
}

/** Values for the {placeholders} in error messages. */
export function limitParams(l: ServerLimits): { maxChars: number; maxMB: number; maxRoles: number } {
  return {
    maxChars: l.messageChars,
    maxMB: Math.round(l.uploadBytes / (1024 * 1024)),
    maxRoles: l.spaceRoles,
  };
}
