// Tray menu rows "who is in voice": one row per occupied voice
// channel of my spaces, own channel first. Pure function, built from the
// presence data the client already has.
import type { ID, VoiceState } from "./api/types";

export interface TrayVoice {
  id: ID;
  label: string;
  current: boolean;
}

export const MAX_TRAY_VOICE = 8;
const MAX_NAMES = 4;
const MAX_LABEL = 80;

export function trayVoiceRows(
  states: VoiceState[],
  channel: (id: ID) => { name: string; space: string } | null,
  userName: (id: ID) => string,
  current: ID | null,
): TrayVoice[] {
  const byChannel = new Map<ID, ID[]>();
  for (const v of states) {
    if (!v.channelId) continue;
    const list = byChannel.get(v.channelId) ?? [];
    list.push(v.userId);
    byChannel.set(v.channelId, list);
  }
  const rows: (TrayVoice & { n: number })[] = [];
  for (const [id, users] of byChannel) {
    const c = channel(id);
    if (!c) continue; // DM calls have their own UI
    const names = users.map(userName).sort((a, b) => a.localeCompare(b));
    const shown = names.slice(0, MAX_NAMES).join(", ");
    const more = names.length > MAX_NAMES ? ` +${names.length - MAX_NAMES}` : "";
    let label = `${c.space} · ${c.name}: ${shown}${more}`;
    if (label.length > MAX_LABEL) label = `${label.slice(0, MAX_LABEL - 1)}…`;
    // "&" marks a mnemonic in Windows menus.
    rows.push({ id, label: label.replaceAll("&", "&&"), current: id === current, n: users.length });
  }
  rows.sort((a, b) => Number(b.current) - Number(a.current) || b.n - a.n || a.label.localeCompare(b.label));
  return rows.slice(0, MAX_TRAY_VOICE).map(({ id, label, current }) => ({ id, label, current }));
}
