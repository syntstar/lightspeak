<script lang="ts">
  // In-app card for calls ringing at us (the Windows toast covers the tray case).
  import Phone from "@lucide/svelte/icons/phone";
  import PhoneOff from "@lucide/svelte/icons/phone-off";
  import { app, api } from "../lib/state/app.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { calls } from "../lib/voice/calls.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";

  const list = $derived(Object.values(calls.incoming).filter((c) => app.channelId !== c.channelId));
</script>

{#if list.length}
  <div class="stack" role="alertdialog" aria-label={t("call.incoming")}>
    {#each list as c (c.channelId)}
      {@const u = app.user(c.fromUserId)}
      {@const ch = app.channels[c.channelId]}
      <div class="card">
        <Avatar
          name={u?.displayName ?? "?"}
          id={c.fromUserId}
          src={u?.avatarUrl && api.url(u.avatarUrl)}
          size={36}
        />
        <button type="button" class="who" onclick={() => app.selectChannel(c.channelId)}>
          <span class="nm">{ch?.kind === "group_dm" ? app.channelTitle(ch) : (u?.displayName ?? "?")}</span>
          <span class="sub">{t("call.incoming")}</span>
        </button>
        <IconButton label={t("call.decline")} danger onclick={() => void calls.decline(c.channelId)}>
          <PhoneOff size={18} strokeWidth={1.75} />
        </IconButton>
        <span class="accept">
          <IconButton label={t("call.accept")} onclick={() => void calls.accept(c.channelId)}>
            <Phone size={18} strokeWidth={1.75} />
          </IconButton>
        </span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .stack {
    position: fixed;
    top: 48px;
    left: 50%;
    translate: -50% 0;
    z-index: 900;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .card {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 320px;
    padding: 10px 10px 10px 12px;
    border-radius: var(--r-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    animation: drop var(--dur-base) var(--ease-spring);
  }
  @keyframes drop {
    from {
      opacity: 0;
      translate: 0 -8px;
    }
  }
  .who {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    padding: 0;
    border: none;
    background: none;
    text-align: left;
    line-height: 1.25;
  }
  .nm {
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub {
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
  .accept :global(button) {
    background: var(--success);
    color: #fff;
  }
</style>
