<script lang="ts">
  // Home → friends: online / all / pending / blocked, add by username.
  import MessageSquare from "@lucide/svelte/icons/message-square";
  import Check from "@lucide/svelte/icons/check";
  import X from "@lucide/svelte/icons/x";
  import MoreHorizontal from "@lucide/svelte/icons/ellipsis";
  import UsersRound from "@lucide/svelte/icons/users-round";
  import type { Relationship } from "../lib/api/types";
  import { errorCode } from "../lib/api/client";
  import { app, api } from "../lib/state/app.svelte";
  import { t, terr } from "../lib/i18n/index.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import { userMenuItems } from "../lib/userMenu";
  import Avatar from "../lib/ui/Avatar.svelte";
  import Button from "../lib/ui/Button.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import SegmentedControl from "../lib/ui/SegmentedControl.svelte";

  type Tab = "online" | "all" | "pending" | "blocked";
  let tab = $state<Tab>("online");
  let username = $state("");
  let adding = $state(false);
  let addMsg = $state<{ text: string; ok: boolean } | null>(null);

  const rels = $derived(Object.values(app.relationships));
  const pendingCount = $derived(rels.filter((r) => r.kind === "pending_in").length);
  const list = $derived.by((): Relationship[] => {
    const byName = (a: Relationship, b: Relationship) =>
      (app.user(a.userId) ?? a.user).displayName.localeCompare((app.user(b.userId) ?? b.user).displayName);
    let l: Relationship[];
    if (tab === "online")
      l = rels.filter((r) => r.kind === "friend" && !["offline", "invisible"].includes(app.status(r.userId)));
    else if (tab === "all") l = rels.filter((r) => r.kind === "friend");
    else if (tab === "pending") l = rels.filter((r) => r.kind === "pending_in" || r.kind === "pending_out");
    else l = rels.filter((r) => r.kind === "blocked");
    return l.sort(byName);
  });

  async function add(e: SubmitEvent) {
    e.preventDefault();
    const name = username.trim().replace(/^@/, "");
    if (!name) return;
    adding = true;
    addMsg = null;
    try {
      const kind = await app.addFriend(name);
      addMsg = {
        text: kind === "friend" ? t("friends.nowFriends", { name }) : t("friends.requestSentTo", { name }),
        ok: true,
      };
      username = "";
    } catch (err) {
      const code = errorCode(err);
      addMsg = { text: code === "unknown_user" ? t("friends.notFound") : terr(code), ok: false };
    } finally {
      adding = false;
    }
  }

  function act(p: Promise<unknown>) {
    p.catch((e: unknown) => toast(terr(errorCode(e)), "error"));
  }

  function sub(r: Relationship): string {
    if (r.kind === "pending_in") return t("friends.incoming");
    if (r.kind === "pending_out") return t("friends.outgoing");
    if (r.kind === "blocked") return t("friends.blocked");
    const p = app.presences[r.userId];
    return p?.statusText || t(`status.${app.status(r.userId)}`);
  }

  const empty = $derived(
    tab === "online"
      ? t("friends.emptyOnline")
      : tab === "all"
        ? t("friends.emptyAll")
        : tab === "pending"
          ? t("friends.emptyPending")
          : t("friends.emptyBlocked"),
  );
</script>

<div class="friends">
  <form class="add" onsubmit={add}>
    <label for="friend-add">{t("friends.add")}</label>
    <p class="hint">{t("friends.addHint")}</p>
    <div class="add-row">
      <input
        id="friend-add"
        bind:value={username}
        placeholder={t("friends.addPlaceholder")}
        autocomplete="off"
        spellcheck="false"
        maxlength="32"
      />
      <Button variant="primary" type="submit" busy={adding} disabled={!username.trim()}
        >{t("friends.sendRequest")}</Button
      >
    </div>
    {#if addMsg}<p class="msg" class:ok={addMsg.ok} role="status">{addMsg.text}</p>{/if}
  </form>

  <div class="tabs">
    <SegmentedControl
      label={t("nav.friends")}
      bind:value={tab}
      options={[
        { value: "online", label: t("friends.online") },
        { value: "all", label: t("friends.all") },
        {
          value: "pending",
          label: pendingCount ? `${t("friends.pending")} (${pendingCount})` : t("friends.pending"),
        },
        { value: "blocked", label: t("friends.blockedTab") },
      ]}
    />
  </div>

  <ul class="list">
    {#each list as r (r.userId)}
      {@const u = app.user(r.userId) ?? r.user}
      <li oncontextmenu={(e) => openMenu(e, userMenuItems(r.userId))}>
        <Avatar
          name={u.displayName}
          id={u.id}
          src={u.avatarUrl && api.url(u.avatarUrl)}
          size={32}
          status={r.kind === "friend" ? app.status(r.userId) : undefined}
        />
        <span class="who">
          <span class="dn">{u.displayName} <span class="un">@{u.username}</span></span>
          <span class="st">{sub(r)}</span>
        </span>
        <span class="actions">
          {#if r.kind === "friend"}
            <IconButton label={t("user.message")} onclick={() => act(app.openDM([r.userId]))}>
              <MessageSquare size={16} strokeWidth={1.75} />
            </IconButton>
          {:else if r.kind === "pending_in"}
            <IconButton label={t("friends.accept")} onclick={() => act(app.setRelation(r.userId, "friend"))}>
              <Check size={16} strokeWidth={2} />
            </IconButton>
            <IconButton label={t("friends.decline")} danger onclick={() => act(app.removeRelation(r.userId))}>
              <X size={16} strokeWidth={2} />
            </IconButton>
          {:else if r.kind === "pending_out"}
            <IconButton
              label={t("friends.cancelRequest")}
              danger
              onclick={() => act(app.removeRelation(r.userId))}
            >
              <X size={16} strokeWidth={2} />
            </IconButton>
          {:else}
            <Button variant="plain" onclick={() => act(app.removeRelation(r.userId))}
              >{t("user.unblock")}</Button
            >
          {/if}
          <IconButton label={t("common.more")} onclick={(e) => openMenu(e, userMenuItems(r.userId))}>
            <MoreHorizontal size={16} strokeWidth={1.75} />
          </IconButton>
        </span>
      </li>
    {:else}
      <li class="empty">
        <UsersRound size={36} strokeWidth={1.25} />
        <span>{empty}</span>
      </li>
    {/each}
  </ul>
</div>

<style>
  .friends {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 20px 24px 0;
    gap: 16px;
    max-width: 760px;
    width: 100%;
    margin: 0 auto;
  }
  .add label {
    font-weight: 650;
    font-size: var(--fs-title3);
  }
  .hint {
    margin: 4px 0 10px;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .add-row {
    display: flex;
    gap: 8px;
  }
  .add-row input {
    flex: 1;
    min-width: 0;
    height: 32px;
    padding: 0 10px;
    border: 1px solid var(--border-hairline);
    border-radius: var(--r-md);
    background: var(--bg-input);
    color: var(--text-1);
  }
  .add-row input:focus {
    outline: none;
    border-color: color-mix(in srgb, var(--accent) 70%, transparent);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 35%, transparent);
  }
  .msg {
    margin: 8px 0 0;
    font-size: var(--fs-callout);
    color: var(--danger);
  }
  .msg.ok {
    color: var(--success-text);
  }
  .tabs {
    display: flex;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0 0 16px;
    overflow-y: auto;
    flex: 1;
    min-height: 0;
  }
  li {
    display: flex;
    align-items: center;
    gap: 12px;
    height: 56px;
    padding: 0 8px;
    border-top: 1px solid var(--separator);
  }
  li:hover {
    background: var(--bg-hover);
    border-radius: var(--r-md);
  }
  .who {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    line-height: 1.3;
  }
  .dn,
  .st {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dn {
    font-weight: 600;
  }
  .un {
    font-weight: 400;
    color: var(--text-3);
    font-size: var(--fs-callout);
  }
  .st {
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .actions {
    display: flex;
    gap: 4px;
  }
  li.empty {
    flex-direction: column;
    justify-content: center;
    height: auto;
    padding: 48px 0;
    border: none;
    color: var(--text-2);
    gap: 10px;
  }
  li.empty:hover {
    background: none;
  }
  li.empty :global(svg) {
    color: var(--text-3);
  }
</style>
