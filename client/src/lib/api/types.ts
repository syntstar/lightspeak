import type { ServerLimits } from "../limits";
// Mirrors the JSON shapes in server/internal/server/*.go (IDs are strings).

export type ID = string;
export type Status = "online" | "idle" | "dnd" | "invisible" | "offline";
export type Role = "owner" | "admin" | "member";
export type ChannelKind = "text" | "voice" | "category" | "dm" | "group_dm";

export interface User {
  id: ID;
  username: string;
  displayName: string;
  avatarUrl?: string;
  /** The account deleted itself. */
  deleted?: boolean;
}

export interface Me extends User {
  status: Status;
  statusText: string;
  isServerAdmin: boolean;
  createdAt: number;
}

export interface Space {
  id: ID;
  name: string;
  iconUrl?: string;
  ownerId: ID;
  /** Legacy; permissions come from roles (lib/perms.ts). */
  role: Role;
  e2eeEpoch: number;
  roles: SpaceRole[];
  everyonePerms: number;
  /** The current user's roles in this space. */
  roleIds: ID[];
}

export interface SpaceRole {
  id: ID;
  name: string;
  /** 1 = lowest; @everyone (id = space id) is 0 and not listed. */
  position: number;
  permissions: number;
}

export interface Channel {
  id: ID;
  spaceId?: ID;
  kind: ChannelKind;
  parentId?: ID;
  name: string;
  topic: string;
  position: number;
  userLimit?: number;
  lastMessageId?: ID;
  ownerId?: ID;
  e2eeEpoch?: number;
  memberIds?: ID[];
  /** Space channels: voice "away" is the away channel. */
  mode?: "open" | "away";
  /** Seconds between two messages of one person (text). */
  slowmode?: number;
  /** Away channel: minutes without activity before clients move there. */
  awayAfter?: number;
  /** Voice: microphone quality. */
  audio?: "speech" | "high" | "music";
  /** Chosen icon (text/voice); absent = # / speaker. */
  icon?: string;
  /** Permission overrides per role (roleId = spaceId for @everyone). */
  overrides?: Override[];
}

export interface Override {
  roleId: ID;
  allow: number;
  deny: number;
}

export interface ReadState {
  channelId: ID;
  lastReadId: ID;
  mentionCount: number;
}

export interface Presence {
  userId: ID;
  status: Status;
  statusText: string;
}

export interface VoiceState {
  userId: ID;
  channelId: ID | null;
  spaceId?: ID;
  muted: boolean;
  deafened: boolean;
  streaming: boolean;
  serverMuted: boolean;
  /** Only listening: no Speak permission in the channel, or the away channel. */
  listener?: boolean;
  /** No Stream permission in the channel. */
  noScreen?: boolean;
  /** Streamers (same channel) this user is watching. */
  watching?: ID[];
  /** Own stream while streaming: full layer height, chosen fps, still ramping up. */
  stream?: StreamInfo;
}

export interface StreamInfo {
  height: number;
  fps: number;
  /** The full layer has not reached its target bitrate yet (start, quality change, bad uplink). */
  ramp: boolean;
}

export interface Relationship {
  userId: ID;
  kind: "friend" | "blocked" | "pending_in" | "pending_out";
  user: User;
}

export interface Member {
  spaceId: ID;
  userId: ID;
  role: Role;
  roleIds: ID[];
  serverAdmin?: boolean;
  joinedAt: number;
  user: User;
  presence?: Presence;
}

export interface Attachment {
  id: ID;
  filename: string;
  mime: string;
  size: number;
  width?: number;
  height?: number;
  url: string;
  thumbUrl?: string;
}

export interface Reaction {
  emoji: string;
  count: number;
  me: boolean;
}

export interface Message {
  id: ID;
  channelId: ID;
  authorId: ID;
  author: User;
  content: string;
  replyToId?: ID;
  referencedMessage?: { id: ID; authorId: ID; content: string; deleted?: boolean };
  mentions: ID[];
  mentionsEveryone: boolean;
  attachments: Attachment[];
  reactions: Reaction[];
  pinned: boolean;
  editedAt?: number;
  createdAt: number;
  nonce?: string;
}

export interface Ready {
  sessionId: string;
  protocolVersion: number;
  minClientVersion: string;
  serverVersion: string;
  /** Operator settings; older servers omit them. */
  limits?: Partial<ServerLimits>;
  user: Me;
  deviceId: ID;
  spaces: Space[];
  channels: Channel[];
  dms: Channel[];
  users: User[];
  readStates: ReadState[];
  presences: Presence[];
  voiceStates: VoiceState[];
  relationships: Relationship[];
  settings: Record<string, unknown>;
  pendingRotations: ID[];
}

export interface AuthResponse {
  token: string;
  user: Me;
  device: { id: ID; name: string };
}

export interface InvitePreview {
  code: string;
  expiresAt: number | null;
  space?: { id: ID; name: string; iconUrl?: string; memberCount: number };
  /** Whether this invite may create a new account (server signup policy). */
  signup?: boolean;
}
