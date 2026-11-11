<script lang="ts">
  // Strip above a DM chat while a call is running there: participants with
  // speaking rings and join/leave.
  import Phone from "@lucide/svelte/icons/phone";
  import PhoneOff from "@lucide/svelte/icons/phone-off";
  import MicOff from "@lucide/svelte/icons/mic-off";
  import HeadphoneOff from "@lucide/svelte/icons/headphone-off";
  import type { Channel } from "../lib/api/types";
  import { app, api } from "../lib/state/app.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { userMenuItems } from "../lib/userMenu";
  import { calls } from "../lib/voice/calls.svelte";
  import { voice } from "../lib/voice/voice.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import Button from "../lib/ui/Button.svelte";

  let { channel }: { channel: Channel } = $props();

  const members = $derived(app.voiceMembers(channel.id));
  const here = $derived(voice.channelId === channel.id);
  const ringing = $derived(calls.outgoing === channel.id);
  const incoming = $derived(calls.incoming[channel.id]);
</script>

{#if members.length || here || incoming}
  <div class="bar" role="region" aria-label={t("call.title")}>
    <div class="people">
      {#each members as v (v.userId)}
        {@const u = app.user(v.userId)}
        <div class="pp">
          <button
            type="button"
            class="p"
            title={u?.displayName}
            onclick={(e) => openMenu(e, userMenuItems(v.userId))}
            oncontextmenu={(e) => openMenu(e, userMenuItems(v.userId))}
          >
            <Avatar
              name={u?.displayName ?? "?"}
              id={v.userId}
              src={u?.avatarUrl && api.url(u.avatarUrl)}
              size={44}
              speaking={!!voice.speaking[v.userId] && !v.muted && !v.serverMuted}
            />
            {#if v.deafened}<span class="st"><HeadphoneOff size={12} strokeWidth={2} /></span
              >{:else if v.muted}<span class="st"><MicOff size={12} strokeWidth={2} /></span>{/if}
          </button>
          {#if v.streaming}
            <button
              type="button"
              class="live"
              aria-label={t("stream.watchUser", { name: u?.displayName ?? t("common.unknownUser") })}
              title={t("stream.watch")}
              onclick={() =>
                voice.stream.isWatching(v.userId)
                  ? voice.stream.open(v.userId)
                  : void voice.stream.watch(v.userId)}>LIVE</button
            >
          {/if}
        </div>
      {/each}
    </div>
    <p class="label">
      {#if incoming && !here}{t("call.incoming")}{:else if ringing}{t("call.ringing")}{:else if here}{t(
          "call.inCall",
        )}{:else}{t("call.running")}{/if}
    </p>
    <div class="actions">
      {#if here}
        <Button variant="danger" onclick={() => void voice.leave()}>
          <PhoneOff size={14} strokeWidth={2} />{t("voice.disconnect")}
        </Button>
      {:else if incoming}
        <Button variant="primary" onclick={() => void calls.accept(channel.id)}>
          <Phone size={14} strokeWidth={2} />{t("call.accept")}
        </Button>
        <Button variant="danger" onclick={() => void calls.decline(channel.id)}>
          <PhoneOff size={14} strokeWidth={2} />{t("call.decline")}
        </Button>
      {:else}
        <Button variant="primary" onclick={() => void voice.join(channel.id)}>
          <Phone size={14} strokeWidth={2} />{t("call.join")}
        </Button>
      {/if}
    </div>
  </div>
{/if}

<style>
  .bar {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 16px 16px 12px;
    background: var(--bg-sidebar);
    border-bottom: 1px solid var(--separator);
    flex: none;
  }
  .people {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 12px;
  }
  .p {
    position: relative;
    padding: 0;
    border: none;
    background: none;
    border-radius: 50%;
  }
  .pp {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
  }
  .live {
    position: absolute;
    left: 50%;
    bottom: -8px;
    transform: translateX(-50%);
    padding: 1px 5px;
    border: none;
    border-radius: var(--r-xs);
    background: var(--live);
    color: #fff;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.04em;
    box-shadow: 0 0 0 2px var(--bg-sidebar);
  }
  .st {
    position: absolute;
    right: -2px;
    bottom: -2px;
    display: inline-flex;
    padding: 3px;
    border-radius: 50%;
    background: var(--bg-elevated);
    color: var(--danger);
    box-shadow: 0 0 0 2px var(--bg-sidebar);
  }
  .label {
    margin: 0;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
