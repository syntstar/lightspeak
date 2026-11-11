<script lang="ts">
  // "Create a space" and "Join with invite" sheets.
  import { app, api } from "../lib/state/app.svelte";
  import { errorCode } from "../lib/api/client";
  import { parseInvite, serverHost } from "../lib/server";
  import type { Channel, InvitePreview, Space } from "../lib/api/types";
  import { t, terr, tp } from "../lib/i18n/index.svelte";
  import Button from "../lib/ui/Button.svelte";
  import Sheet from "../lib/ui/Sheet.svelte";
  import TextField from "../lib/ui/TextField.svelte";
  import { toast } from "../lib/ui/toast.svelte";

  let {
    create = $bindable(false),
    join = $bindable(false),
    joinCode = $bindable(""),
  }: {
    create?: boolean;
    join?: boolean;
    joinCode?: string;
  } = $props();

  let name = $state("");
  let busy = $state(false);
  let error = $state<string | null>(null);
  let preview = $state<InvitePreview | null>(null);

  const invite = $derived(parseInvite(joinCode));
  /** Invite link of another server: handled by the server switch dialog. */
  const elsewhere = $derived(invite?.server && invite.server !== app.server ? invite.server : null);
  const code = $derived(invite && !elsewhere ? invite.code : null);

  function followElsewhere() {
    app.pendingInvite = joinCode.trim();
    join = false;
    joinCode = "";
  }

  $effect(() => {
    if (create) {
      name = "";
      error = null;
    }
  });

  $effect(() => {
    const c = code;
    preview = null;
    error = null;
    if (!join || !c) return;
    const ctl = new AbortController();
    api
      .request<InvitePreview>(`/invites/${c}`, { signal: ctl.signal })
      .then((p) => (preview = p))
      .catch((e: unknown) => {
        if (!(e instanceof DOMException)) error = terr(errorCode(e));
      });
    return () => ctl.abort();
  });

  async function doCreate(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = null;
    try {
      const res = await api.post<{ space: Space; channels: Channel[] }>("/spaces", {
        name: name.trim(),
        // Real, editable categories, named in the creator's language.
        channels: [
          { kind: "category", name: t("nav.text") },
          { kind: "text", name: t("space.defaultText"), parent: 0 },
          { kind: "category", name: t("nav.voice") },
          { kind: "voice", name: t("space.defaultVoice"), parent: 2 },
        ],
      });
      app.spaces[res.space.id] = res.space;
      if (!app.spaceOrder.includes(res.space.id)) app.spaceOrder = [...app.spaceOrder, res.space.id];
      for (const c of res.channels) app.channels[c.id] = c;
      app.selectSpace(res.space.id);
      create = false;
    } catch (err) {
      error = terr(errorCode(err));
    } finally {
      busy = false;
    }
  }

  async function doJoin(e: SubmitEvent) {
    e.preventDefault();
    if (!code) return;
    busy = true;
    error = null;
    try {
      const res = await api.post<{ space: Space; channels: Channel[] }>(`/invites/${code}/accept`);
      app.spaces[res.space.id] = res.space;
      if (!app.spaceOrder.includes(res.space.id)) app.spaceOrder = [...app.spaceOrder, res.space.id];
      for (const c of res.channels ?? []) app.channels[c.id] = c;
      app.selectSpace(res.space.id);
      toast(t("auth.joined", { space: res.space.name }), "success");
      join = false;
      joinCode = "";
    } catch (err) {
      error = terr(errorCode(err));
    } finally {
      busy = false;
    }
  }
</script>

<Sheet bind:open={create} title={t("space.create")}>
  <form id="create-space" onsubmit={doCreate}>
    <p class="sub">{t("space.createSub")}</p>
    <TextField label={t("space.name")} bind:value={name} maxlength={64} {error} autofocus />
  </form>
  {#snippet footer()}
    <Button onclick={() => (create = false)}>{t("common.cancel")}</Button>
    <Button variant="primary" type="submit" form="create-space" {busy} disabled={!name.trim()}
      >{t("common.create")}</Button
    >
  {/snippet}
</Sheet>

<Sheet bind:open={join} title={t("space.join")}>
  <form id="join-space" onsubmit={doJoin}>
    <p class="sub">{t("space.joinSub")}</p>
    <TextField
      label={t("auth.invite")}
      placeholder={t("auth.invitePlaceholder")}
      bind:value={joinCode}
      error={error ?? (joinCode && !invite ? terr("invite_invalid") : null)}
      autofocus
      spellcheck="false"
    />
    {#if elsewhere}
      <p class="sub">{t("server.inviteOther", { host: serverHost(elsewhere) })}</p>
      <Button onclick={followElsewhere}>{t("server.switchTo", { host: serverHost(elsewhere) })}</Button>
    {/if}
    {#if preview?.space}
      <p class="preview">
        <strong>{preview.space.name}</strong> · {tp("auth.inviteMembers", preview.space.memberCount)}
      </p>
    {/if}
  </form>
  {#snippet footer()}
    <Button onclick={() => (join = false)}>{t("common.cancel")}</Button>
    <Button variant="primary" type="submit" form="join-space" {busy} disabled={!code || !preview?.space}>
      {preview?.space ? t("auth.joinSpace", { space: preview.space.name }) : t("space.join")}
    </Button>
  {/snippet}
</Sheet>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
  }
  .sub {
    margin: 0;
    color: var(--text-2);
  }
  .preview {
    margin: 0;
    color: var(--text-2);
  }
</style>
