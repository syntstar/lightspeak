<script lang="ts">
  // Logged-in layout: rail · sidebar · content (title bar + view) · members.
  import Hash from "@lucide/svelte/icons/hash";
  import Megaphone from "@lucide/svelte/icons/megaphone";
  import { restrictedForAll } from "../lib/channelRules";
  import { P } from "../lib/perms";
  import AtSign from "@lucide/svelte/icons/at-sign";
  import Users from "@lucide/svelte/icons/users";
  import PinIcon from "@lucide/svelte/icons/pin";
  import PinsSheet from "./chat/PinsSheet.svelte";
  import Search from "@lucide/svelte/icons/search";
  import WifiOff from "@lucide/svelte/icons/wifi-off";
  import ChevronDown from "@lucide/svelte/icons/chevron-down";
  import Copy from "@lucide/svelte/icons/copy";
  import CheckCheck from "@lucide/svelte/icons/check-check";
  import Compass from "@lucide/svelte/icons/compass";
  import { app, api, HOME } from "../lib/state/app.svelte";
  import { prefs, savePrefs } from "../lib/prefs.svelte";
  import { t, terr } from "../lib/i18n/index.svelte";
  import { errorCode } from "../lib/api/client";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import { notifyMenuItems } from "../lib/notifyMenu";
  import { spaceAdmin, spaceAdminItems } from "../lib/spaceAdmin.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import Kbd from "../lib/ui/Kbd.svelte";
  import Spinner from "../lib/ui/Spinner.svelte";
  import Button from "../lib/ui/Button.svelte";
  import CaptionButtons from "./CaptionButtons.svelte";
  import ChannelList from "./ChannelList.svelte";
  import MemberList from "./MemberList.svelte";
  import Rail from "./Rail.svelte";
  import SpaceSheets from "./SpaceSheets.svelte";
  import UserPanel from "./UserPanel.svelte";
  import ChatView from "./chat/ChatView.svelte";
  import FriendsView from "./FriendsView.svelte";
  import NewDMSheet from "./NewDMSheet.svelte";
  import UsersRound from "@lucide/svelte/icons/users-round";
  import SquarePen from "@lucide/svelte/icons/square-pen";
  import Phone from "@lucide/svelte/icons/phone";
  import VoicePanel from "./VoicePanel.svelte";
  import CallBar from "./CallBar.svelte";
  import IncomingCalls from "./IncomingCalls.svelte";
  import { calls } from "../lib/voice/calls.svelte";
  import { voice } from "../lib/voice/voice.svelte";
  import { channelIcon } from "../lib/channelIcons";

  let { joinCode = $bindable(""), joinOpen = $bindable(false) }: { joinCode?: string; joinOpen?: boolean } =
    $props();
  let createOpen = $state(false);
  let newDM = $state(false);

  const ch = $derived(app.channel);
  const sv = voice.stream;
  const streamView = $derived(sv.view && sv.watching.length > 0);
  const theater = $derived(streamView && sv.theater);
  const voiceCh = $derived(voice.channelId ? app.channels[voice.channelId] : undefined);
  // Picking another channel leaves the stream view (the stream keeps
  // playing its sound; the video pauses with nothing attached).
  let lastChannel = app.channelId;
  $effect(() => {
    const c = app.channelId;
    if (c !== lastChannel) {
      lastChannel = c;
      sv.close();
    }
  });
  const isDM = $derived(ch?.kind === "dm" || ch?.kind === "group_dm");
  // Narrow windows (< 1100 px) hide the member list by default so the chat
  // keeps a readable width; the toggle still shows it for this session.
  const NARROW = 1100;
  let winWidth = $state(window.innerWidth);
  let narrowMembers = $state(false);
  const narrow = $derived(winWidth < NARROW);
  const membersOn = $derived(narrow ? narrowMembers : prefs.showMembers);
  const showMembers = $derived(membersOn && app.spaceId !== HOME && !app.searchOpen);
  const friendsView = $derived(app.spaceId === HOME && !ch);
  const peerRel = $derived(
    ch?.kind === "dm" && app.dmPeer(ch) ? app.relation(app.dmPeer(ch)!.id) : undefined,
  );
  const blockedPeer = $derived(ch?.kind === "dm" && peerRel === "blocked");
  /** 1:1 DMs and calls only between friends; history stays readable. */
  const strangerPeer = $derived(
    ch?.kind === "dm" && !!app.dmPeer(ch) && !blockedPeer && peerRel !== "friend",
  );
  async function befriendPeer() {
    const peer = ch ? app.dmPeer(ch) : undefined;
    if (!peer) return;
    try {
      await app.setRelation(peer.id, "friend");
      toast(t("friends.requestSent"), "success");
    } catch (e) {
      toast(terr(errorCode(e)), "error");
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.ctrlKey && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "f" && ready) {
      e.preventDefault();
      app.openSearch();
    }
  }
  const ready = $derived(app.me !== null);

  function spaceHeaderMenu(e: MouseEvent) {
    const sp = app.space;
    if (!sp) return;
    const admin = spaceAdminItems(sp.id);
    openMenu(e, [
      ...admin,
      ...(admin.length ? [{ separator: true as const }] : []),
      {
        label: t("space.markRead"),
        icon: CheckCheck,
        action: () => {
          for (const c of app.spaceChannels(sp.id)) {
            if (app.isUnread(c) && c.lastMessageId)
              void api.post(`/channels/${c.id}/ack`, { messageId: c.lastMessageId }).catch(() => {});
          }
        },
      },
      { separator: true },
      ...notifyMenuItems({ kind: "space", id: sp.id }),
      { separator: true },
      {
        label: t("space.copyId"),
        icon: Copy,
        action: () =>
          void navigator.clipboard.writeText(sp.id).then(() => toast(t("toast.copied"), "success")),
      },
    ]);
  }

  function placeholder(): string {
    if (!ch) return t("nav.selectChannel");
    if (ch.kind === "dm") return t("nav.dmPlaceholder", { name: app.channelTitle(ch) });
    if (ch.kind === "group_dm") return t("nav.groupDmPlaceholder", { name: app.channelTitle(ch) });
    return t("nav.channelPlaceholder", { name: ch.name });
  }
  let pinsOpen = $state(false);
</script>

<div class="shell" class:theater class:fullscreen={theater && sv.fullscreen}>
  {#if !theater}<Rail onCreate={() => (createOpen = true)} onJoin={() => (joinOpen = true)} />{/if}

  <div class="sidebar">
    {#if app.spaceId === HOME}
      <div class="sb-head" data-tauri-drag-region>
        <span class="sb-title" data-tauri-drag-region>{t("nav.dms")}</span>
      </div>
    {:else}
      <button type="button" class="sb-head btn" onclick={spaceHeaderMenu} aria-haspopup="menu">
        <span class="sb-title">{app.space?.name ?? ""}</span>
        <ChevronDown size={16} strokeWidth={1.75} />
      </button>
    {/if}
    <button type="button" class="qs-btn" onclick={() => (app.quickSwitcher = true)}>
      <Search size={14} strokeWidth={1.75} />
      <span>{t("nav.quickSwitch")}</span>
      <Kbd keys={[t("kbd.ctrl"), "K"]} />
    </button>
    {#if ready}
      <ChannelList onNewDM={() => (newDM = true)} />
    {:else}
      <div class="fill"></div>
    {/if}
    <VoicePanel />
    <UserPanel />
  </div>

  <div class="content">
    <header class="titlebar" data-tauri-drag-region>
      {#if streamView && voiceCh}
        {@const Icon = channelIcon("voice", voiceCh.icon)}
        <span class="ch-icon"><Icon size={18} strokeWidth={1.75} /></span>
        <h1 data-tauri-drag-region>{app.channelTitle(voiceCh)}</h1>
      {:else if friendsView}
        <span class="ch-icon"><UsersRound size={18} strokeWidth={1.75} /></span>
        <h1 data-tauri-drag-region>{t("nav.friends")}</h1>
      {:else if ch}
        <span class="ch-icon"
          >{#if isDM}<AtSign size={18} strokeWidth={1.75} />{:else if ch.icon}{@const Icon = channelIcon(
              "text",
              ch.icon,
            )}<Icon size={18} strokeWidth={1.75} />{:else if restrictedForAll(ch, P.send)}<Megaphone
              size={18}
              strokeWidth={1.75}
            />{:else}<Hash size={18} strokeWidth={1.75} />{/if}</span
        >
        <h1 data-tauri-drag-region>{app.channelTitle(ch)}</h1>
        {#if ch.topic}<span class="topic" data-tauri-drag-region>{ch.topic}</span>{/if}
      {/if}
      <span class="spacer" data-tauri-drag-region></span>
      {#if friendsView}
        <Button variant="primary" onclick={() => (newDM = true)}>
          <SquarePen size={14} strokeWidth={2} />{t("dm.new")}
        </Button>
      {/if}
      {#if ch && isDM && !blockedPeer && !strangerPeer && voice.channelId !== ch.id}
        <IconButton label={t("call.start")} onclick={() => void calls.start(ch.id)}>
          <Phone size={18} strokeWidth={1.75} />
        </IconButton>
      {/if}
      {#if ready}
        <IconButton
          label={`${t("search.title")} (${t("kbd.ctrl")}+F)`}
          active={app.searchOpen}
          onclick={() => (app.searchOpen ? (app.searchOpen = false) : app.openSearch())}
        >
          <Search size={18} strokeWidth={1.75} />
        </IconButton>
      {/if}
      {#if ch && ch.kind !== "voice"}
        <IconButton label={t("chat.pins")} onclick={() => (pinsOpen = true)}>
          <PinIcon size={18} strokeWidth={1.75} />
        </IconButton>
      {/if}
      {#if app.spaceId !== HOME}
        <IconButton
          label={t("nav.toggleMembers")}
          active={membersOn}
          onclick={() => {
            if (narrow) {
              narrowMembers = !narrowMembers;
              return;
            }
            prefs.showMembers = !prefs.showMembers;
            savePrefs();
          }}
        >
          <Users size={18} strokeWidth={1.75} />
        </IconButton>
      {/if}
      <CaptionButtons />
    </header>
    {#if ch}<PinsSheet channelId={ch.id} bind:open={pinsOpen} />{/if}

    {#if app.conn === "offline" || app.conn === "reconnecting"}
      <div class="banner" role="status">
        {#if app.conn === "offline"}<WifiOff size={14} strokeWidth={2} />{:else}<Spinner size={12} />{/if}
        <span>{app.conn === "offline" ? t("conn.offline") : t("conn.reconnecting")}</span>
        {#if app.conn === "offline"}
          <Button variant="plain" onclick={() => app.nudge()}>{t("common.retry")}</Button>
        {/if}
      </div>
    {/if}

    <div class="body">
      <main class="view">
        {#if !ready}
          <div class="center"><Spinner size={22} /><span>{t("app.loading")}</span></div>
        {:else if streamView}
          <!-- Lazy: not part of the first screen. -->
          {#await import("./stream/StreamView.svelte") then m}<m.default />{/await}
        {:else if app.spaceOrder.length === 0 && app.spaceId === HOME && !app.dms().length && !Object.keys(app.relationships).length}
          <div class="center empty">
            <Compass size={40} strokeWidth={1.25} />
            <h2>{t("nav.noSpaces")}</h2>
            <p>{t("nav.noSpacesHint")}</p>
            <div class="row">
              <Button variant="primary" onclick={() => (createOpen = true)}>{t("space.create")}</Button>
              <Button onclick={() => (joinOpen = true)}>{t("nav.joinSpace")}</Button>
            </div>
          </div>
        {:else if friendsView}
          <FriendsView />
        {:else if ch && (ch.kind === "text" || isDM)}
          {#if blockedPeer}<div class="blocked-note" role="status">{t("dm.blockedNote")}</div>{/if}
          {#if strangerPeer}<div class="blocked-note" role="status">
              {peerRel === "pending_out" ? t("dm.pendingNote") : t("dm.notFriendsNote")}
              {#if peerRel === "pending_in" || !peerRel}<Button onclick={() => void befriendPeer()}
                  >{peerRel === "pending_in" ? t("friends.accept") : t("user.addFriend")}</Button
                >{/if}
            </div>{/if}
          {#if isDM}<CallBar channel={ch} />{/if}
          {#key ch.id}<ChatView channel={ch} />{/key}
        {:else}
          <div class="center empty">
            {#if ch && !isDM}<span class="big-hash"><Hash size={32} strokeWidth={1.5} /></span>{/if}
            <p>{placeholder()}</p>
          </div>
        {/if}
      </main>
      {#if showMembers && ready && !theater}
        <div class="members-wrap"><MemberList /></div>
      {/if}
      {#if app.searchOpen && ready}
        {#await import("./SearchPanel.svelte") then m}<m.default />{/await}
      {/if}
    </div>
  </div>
</div>

<svelte:window onkeydown={onKey} bind:innerWidth={winWidth} />
{#if spaceAdmin.sheet}
  {#await import("./SpaceAdmin.svelte") then m}<m.default />{/await}
{/if}
<SpaceSheets bind:create={createOpen} bind:join={joinOpen} bind:joinCode />
<NewDMSheet bind:open={newDM} />
<IncomingCalls />
{#if voice.stream.dialog}
  {#await import("./StreamDialog.svelte") then m}<m.default />{/await}
{/if}

<style>
  .shell {
    display: flex;
    height: 100%;
  }
  .sidebar {
    width: var(--sidebar-w);
    flex: none;
    display: flex;
    flex-direction: column;
    background: var(--bg-sidebar);
    border-right: 1px solid var(--separator);
    min-height: 0;
  }
  .sb-head {
    display: flex;
    align-items: center;
    gap: 6px;
    height: var(--titlebar-h);
    padding: 0 16px;
    flex: none;
    border: none;
    background: transparent;
    text-align: left;
    width: 100%;
  }
  .sb-head.btn:hover {
    background: var(--bg-hover);
  }
  .sb-title {
    flex: 1;
    min-width: 0;
    font-weight: 650;
    font-size: var(--fs-headline);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .qs-btn {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 28px;
    margin: 0 10px 4px;
    padding: 0 8px;
    border: none;
    border-radius: var(--r-sm);
    background: var(--bg-input);
    color: var(--text-3);
    font-size: var(--fs-callout);
    flex: none;
  }
  .qs-btn span {
    flex: 1;
    text-align: left;
  }
  .qs-btn:hover {
    color: var(--text-2);
    background: var(--bg-active);
  }
  .fill {
    flex: 1;
  }
  .theater .sidebar {
    display: none;
  }
  .fullscreen .titlebar {
    display: none;
  }
  .content {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg-content);
  }
  .titlebar {
    display: flex;
    align-items: center;
    gap: 8px;
    height: var(--titlebar-h);
    padding-left: 16px;
    border-bottom: 1px solid var(--separator);
    flex: none;
  }
  .ch-icon {
    display: inline-flex;
    color: var(--text-3);
  }
  h1 {
    margin: 0;
    font-size: var(--fs-headline);
    font-weight: 650;
    white-space: nowrap;
  }
  .topic {
    min-width: 0;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    padding-left: 8px;
    border-left: 1px solid var(--separator);
  }
  .spacer {
    flex: 1;
    align-self: stretch;
  }
  .banner {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 30px;
    background: color-mix(in srgb, var(--warning) 18%, var(--bg-content));
    color: var(--text-1);
    font-size: var(--fs-callout);
    flex: none;
  }
  .body {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  .view {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .center {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-2);
    padding: 24px;
    text-align: center;
  }
  .empty h2 {
    margin: 4px 0 0;
    font-size: var(--fs-title2);
    color: var(--text-1);
  }
  .empty p {
    margin: 0;
  }
  .empty :global(svg) {
    color: var(--text-3);
  }
  .big-hash {
    display: inline-flex;
    padding: 14px;
    border-radius: 50%;
    background: var(--bg-active);
  }
  .blocked-note {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-wrap: wrap;
    gap: 10px;
    padding: 8px 16px;
    font-size: var(--fs-callout);
    color: var(--text-2);
    background: var(--bg-active);
    text-align: center;
  }
  .row {
    display: flex;
    gap: 8px;
    margin-top: 8px;
  }
  @media (max-width: 1099px) {
    .members-wrap {
      display: none;
    }
  }
  .members-wrap {
    display: flex;
  }
</style>
