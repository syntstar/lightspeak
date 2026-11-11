<script lang="ts">
  // Bottom of the sidebar: own avatar/status, mute, deafen, settings.
  import Mic from "@lucide/svelte/icons/mic";
  import MicOff from "@lucide/svelte/icons/mic-off";
  import Headphones from "@lucide/svelte/icons/headphones";
  import HeadphoneOff from "@lucide/svelte/icons/headphone-off";
  import Settings from "@lucide/svelte/icons/settings";
  import type { Status } from "../lib/api/types";
  import { app, api } from "../lib/state/app.svelte";
  import { local } from "../lib/state/local.svelte";
  import { voice } from "../lib/voice/voice.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";

  const statuses: Exclude<Status, "offline">[] = ["online", "idle", "dnd", "invisible"];

  function statusMenu(e: MouseEvent) {
    openMenu(e, [
      { heading: t("tray.status") },
      ...statuses.map((s) => ({
        label: t(`status.${s}`),
        checked: app.me?.status === s,
        action: () => {
          local.autoIdle = false;
          app.setStatus(s);
        },
      })),
    ]);
  }
</script>

{#if app.me}
  <div class="panel">
    <button type="button" class="me" onclick={statusMenu} aria-haspopup="menu">
      <Avatar
        name={app.me.displayName}
        id={app.me.id}
        src={app.me.avatarUrl && api.url(app.me.avatarUrl)}
        size={32}
        status={app.me.status === "invisible" ? "offline" : app.me.status}
      />
      <span class="who">
        <span class="dn">{app.me.displayName}</span>
        <span class="st">{app.me.statusText || t(`status.${app.me.status}`)}</span>
      </span>
    </button>
    <IconButton
      label={t("tray.mute")}
      danger={local.muted}
      active={local.muted}
      onclick={() => voice.toggleMute()}
      tip="top"
    >
      {#if local.muted}<MicOff size={18} strokeWidth={1.75} />{:else}<Mic size={18} strokeWidth={1.75} />{/if}
    </IconButton>
    <IconButton
      label={t("tray.deafen")}
      danger={local.deafened}
      active={local.deafened}
      onclick={() => voice.toggleDeafen()}
      tip="top"
    >
      {#if local.deafened}<HeadphoneOff size={18} strokeWidth={1.75} />{:else}<Headphones
          size={18}
          strokeWidth={1.75}
        />{/if}
    </IconButton>
    <IconButton label={t("nav.settings")} onclick={() => (app.settingsOpen = true)} tip="top">
      <Settings size={18} strokeWidth={1.75} />
    </IconButton>
  </div>
{/if}

<style>
  .panel {
    display: flex;
    align-items: center;
    gap: 2px;
    height: 52px;
    padding: 0 8px;
    border-top: 1px solid var(--separator);
    flex: none;
  }
  .me {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    height: 40px;
    padding: 0 6px 0 4px;
    border: none;
    border-radius: var(--r-md);
    background: transparent;
    text-align: left;
  }
  .me:hover {
    background: var(--bg-hover);
  }
  .who {
    display: flex;
    flex-direction: column;
    min-width: 0;
    line-height: 1.2;
  }
  .dn,
  .st {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dn {
    font-weight: 600;
    font-size: var(--fs-body);
  }
  .st {
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
</style>
