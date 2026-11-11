<script lang="ts">
  // Settings → My account: profile (avatar, display name, username), server,
  // password change, the list of signed-in sessions and
  // deleting the account. Guests only see what applies to them: name,
  // avatar and ending their guest access.
  import { isWeb } from "../../lib/native";
  import { onMount } from "svelte";
  import type { ID } from "../../lib/api/types";
  import { errorCode } from "../../lib/api/client";
  import { app, api } from "../../lib/state/app.svelte";
  import { prefs } from "../../lib/prefs.svelte";
  import { formatDay, formatRelative, t, terr } from "../../lib/i18n/index.svelte";
  import { serverHost } from "../../lib/server";
  import Avatar from "../../lib/ui/Avatar.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Sheet from "../../lib/ui/Sheet.svelte";
  import TextField from "../../lib/ui/TextField.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";
  import { toast } from "../../lib/ui/toast.svelte";

  interface Session {
    id: ID;
    deviceName: string;
    createdAt: number;
    lastUsedAt: number;
    userAgent: string;
    current: boolean;
  }

  const MAX_AVATAR = 5 << 20; // server limit (files.go maxAvatarBytes)

  let displayName = $state(app.me?.displayName ?? "");
  let username = $state(app.me?.username ?? "");
  let savingProfile = $state(false);
  let profileError = $state<string | null>(null);
  let avatarBusy = $state(false);
  let fileInput = $state<HTMLInputElement>();

  let pwOpen = $state(false);
  let pwOld = $state("");
  let pwNew = $state("");
  let pwRepeat = $state("");
  let pwRevoke = $state(true);
  let pwBusy = $state(false);
  let pwError = $state<string | null>(null);

  let delOpen = $state(false);
  let delPw = $state("");
  let delBusy = $state(false);
  let delError = $state<string | null>(null);
  /** Spaces the user owns: they must be handed over or deleted first. */
  const owned = $derived(
    app.spaceOrder.flatMap((id) => {
      const s = app.spaces[id];
      return s && s.ownerId === app.me?.id ? [s] : [];
    }),
  );

  let sessions = $state<Session[] | null>(null);
  let sessionsError = $state(false);
  let revoking = $state<ID | "others" | null>(null);

  const dirty = $derived(
    !!app.me &&
      (displayName.trim() !== app.me.displayName || username.trim().toLowerCase() !== app.me.username),
  );
  const others = $derived(sessions?.filter((s) => !s.current) ?? []);

  onMount(() => void loadSessions());

  async function loadSessions() {
    sessionsError = false;
    try {
      sessions = await api.get<Session[]>("/users/@me/sessions");
    } catch {
      sessionsError = true;
    }
  }

  async function saveProfile(e: SubmitEvent) {
    e.preventDefault();
    if (!app.me || !dirty || savingProfile) return;
    const body: { displayName?: string; username?: string } = {};
    if (displayName.trim() !== app.me.displayName) body.displayName = displayName.trim();
    if (username.trim().toLowerCase() !== app.me.username) body.username = username.trim().toLowerCase();
    savingProfile = true;
    profileError = null;
    try {
      const me = await api.patch<typeof app.me>("/users/@me", body);
      if (me) {
        app.me = { ...app.me, ...me };
        displayName = me.displayName;
        username = me.username;
      }
      toast(t("account.saved"), "success");
    } catch (err) {
      profileError = terr(errorCode(err));
    } finally {
      savingProfile = false;
    }
  }

  async function pickAvatar(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file || !app.me) return;
    if (file.size > MAX_AVATAR) {
      toast(t("account.avatarTooLarge"), "error");
      return;
    }
    avatarBusy = true;
    try {
      const r = await api.request<{ avatarUrl: string }>("/users/@me/avatar", {
        method: "POST",
        raw: file,
        headers: { "Content-Type": file.type || "application/octet-stream" },
      });
      app.me = { ...app.me, avatarUrl: r.avatarUrl };
    } catch (err) {
      const code = errorCode(err);
      toast(code === "invalid_image" ? t("account.avatarInvalid") : terr(code), "error");
    } finally {
      avatarBusy = false;
    }
  }

  async function removeAvatar() {
    if (!app.me) return;
    avatarBusy = true;
    try {
      await api.del("/users/@me/avatar");
      app.me = { ...app.me, avatarUrl: undefined };
    } catch (err) {
      toast(terr(errorCode(err)), "error");
    } finally {
      avatarBusy = false;
    }
  }

  /** Signing out ends a guest for good, so ask first. */
  function endGuest() {
    if (confirm(t("account.guestEndConfirm"))) void app.logout();
  }

  function openPassword() {
    pwOld = pwNew = pwRepeat = "";
    pwRevoke = true;
    pwError = null;
    pwOpen = true;
  }

  async function changePassword(e?: SubmitEvent) {
    e?.preventDefault();
    if (pwBusy) return;
    if (pwNew !== pwRepeat) {
      pwError = t("auth.passwordMismatch");
      return;
    }
    pwBusy = true;
    pwError = null;
    try {
      await api.post("/users/@me/password", {
        oldPassword: pwOld,
        newPassword: pwNew,
        revokeOtherSessions: pwRevoke,
      });
      pwOpen = false;
      pwOld = pwNew = pwRepeat = "";
      toast(t("account.pwChanged"), "success");
      if (pwRevoke) void loadSessions();
    } catch (err) {
      const code = errorCode(err);
      pwError = code === "wrong_password" ? t("account.pwWrong") : terr(code);
    } finally {
      pwBusy = false;
    }
  }

  function openDelete() {
    delPw = "";
    delError = null;
    delOpen = true;
  }

  async function deleteAccount(e?: SubmitEvent) {
    e?.preventDefault();
    if (delBusy || owned.length) return;
    delBusy = true;
    delError = null;
    try {
      await api.post("/users/@me/delete", { password: delPw });
      delOpen = false;
      app.settingsOpen = false;
      await app.logout(true);
      app.authNotice = "account.deleted";
    } catch (err) {
      const code = errorCode(err);
      delError = code === "wrong_password" ? t("account.pwWrong") : terr(code);
    } finally {
      delBusy = false;
    }
  }

  async function revoke(id: ID) {
    revoking = id;
    try {
      await api.del(`/users/@me/sessions/${id}`);
      sessions = sessions?.filter((s) => s.id !== id) ?? null;
    } catch (err) {
      toast(terr(errorCode(err)), "error");
    } finally {
      revoking = null;
    }
  }

  async function revokeOthers() {
    revoking = "others";
    try {
      for (const s of others) {
        await api.del(`/users/@me/sessions/${s.id}`).catch((err: unknown) => {
          if (errorCode(err) !== "unknown_session") throw err;
        });
      }
    } catch (err) {
      toast(terr(errorCode(err)), "error");
    } finally {
      revoking = null;
      void loadSessions();
    }
  }
</script>

{#if app.me}
  <div class="group">
    <div class="row profile">
      <Avatar
        name={app.me.displayName}
        id={app.me.id}
        src={app.me.avatarUrl && api.url(app.me.avatarUrl)}
        size={56}
      />
      <div class="pinfo">
        <div class="pname">{app.me.displayName}</div>
        <div class="muted">{app.me.guest ? t("members.guest") : `@${app.me.username}`}</div>
      </div>
      <span class="spacer"></span>
      <input
        bind:this={fileInput}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        hidden
        onchange={pickAvatar}
      />
      {#if app.me.avatarUrl}
        <Button variant="plain" disabled={avatarBusy} onclick={removeAvatar}
          >{t("account.avatarRemove")}</Button
        >
      {/if}
      <Button busy={avatarBusy} onclick={() => fileInput?.click()}>{t("account.avatarChange")}</Button>
    </div>
  </div>

  <form class="group form" onsubmit={saveProfile}>
    <div class="fields" class:single={app.me.guest}>
      <TextField
        label={t("account.displayName")}
        bind:value={displayName}
        maxlength={32}
        autocomplete="nickname"
      />
      {#if !app.me.guest}
        <TextField
          label={t("account.username")}
          bind:value={username}
          maxlength={32}
          autocomplete="username"
          hint={t("account.usernameHint")}
        />
      {/if}
    </div>
    {#if profileError}<p class="err" role="alert">{profileError}</p>{/if}
    <div class="actions">
      <Button
        variant="plain"
        disabled={!dirty || savingProfile}
        onclick={() => {
          displayName = app.me?.displayName ?? "";
          username = app.me?.username ?? "";
          profileError = null;
        }}>{t("common.cancel")}</Button
      >
      <Button type="submit" variant="primary" busy={savingProfile} disabled={!dirty || !displayName.trim()}
        >{t("common.save")}</Button
      >
    </div>
  </form>

  <div class="group">
    <div class="row">
      <span class="two">
        {t("settings.server")}
        <small class="selectable"
          >{prefs.servers[app.server]?.name || serverHost(app.server)} · {serverHost(app.server)}</small
        >
      </span>
      {#if !isWeb}
        <Button
          onclick={() => {
            app.settingsOpen = false;
            void app.switchServer();
          }}>{t("server.switch")}</Button
        >
      {/if}
    </div>
    {#if app.me.guest}
      <div class="row">
        <span class="two">
          {t("account.guest")}
          <small>{t("account.guestHint")}</small>
        </span>
        <Button variant="danger" onclick={endGuest}>{t("account.guestEnd")}</Button>
      </div>
    {:else}
      <div class="row">
        <span class="two">
          {t("account.password")}
          <small>{t("account.passwordHint")}</small>
        </span>
        <Button onclick={openPassword}>{t("account.pwChange")}</Button>
      </div>
      <div class="row">
        <span>{t("settings.loggedInAs", { name: app.me.username })}</span>
        <Button variant="danger" onclick={() => void app.logout()}>{t("settings.logout")}</Button>
      </div>
    {/if}
  </div>
  <p class="note">{t("server.switchNote")}</p>

  <h3 class="gh">{t("account.sessions")}</h3>
  <div class="group">
    {#if sessionsError}
      <div class="row">
        <span class="muted">{t("account.sessionsFailed")}</span>
        <Button onclick={loadSessions}>{t("common.retry")}</Button>
      </div>
    {:else if !sessions}
      <div class="row"><span class="muted">{t("common.loading")}</span></div>
    {:else}
      {#each sessions as s (s.id)}
        <div class="row">
          <span class="two">
            <span>
              {s.deviceName || t("account.unknownDevice")}
              {#if s.current}<span class="badge">{t("account.thisDevice")}</span>{/if}
            </span>
            <small>
              {t("account.sessionMeta", {
                since: formatDay(s.createdAt),
                active: s.current ? t("account.activeNow") : formatRelative(s.lastUsedAt),
              })}
            </small>
          </span>
          {#if !s.current}
            <Button busy={revoking === s.id} disabled={revoking !== null} onclick={() => void revoke(s.id)}
              >{t("account.revoke")}</Button
            >
          {/if}
        </div>
      {/each}
      {#if others.length}
        <div class="row">
          <span class="muted">{t("account.revokeOthersHint")}</span>
          <Button
            variant="danger"
            busy={revoking === "others"}
            disabled={revoking !== null}
            onclick={revokeOthers}>{t("account.revokeOthers")}</Button
          >
        </div>
      {/if}
    {/if}
  </div>

  {#if !app.me.guest}
    <div class="group danger-zone">
      <div class="row">
        <span class="two">
          {t("account.delete")}
          <small>{t("account.deleteHint")}</small>
        </span>
        <Button variant="danger" onclick={openDelete}>{t("account.delete")}</Button>
      </div>
    </div>
  {/if}

  <Sheet bind:open={delOpen} title={t("account.deleteTitle")} width={420}>
    <form class="pw" id="del-form" onsubmit={deleteAccount}>
      {#if owned.length}
        <p class="err" role="alert">
          {t("account.deleteOwns", { spaces: owned.map((s) => s.name).join(", ") })}
        </p>
      {:else}
        <p class="body">{t("account.deleteBody")}</p>
        <TextField
          label={t("account.deletePassword")}
          type="password"
          bind:value={delPw}
          autocomplete="current-password"
          autofocus
        />
      {/if}
      {#if delError}<p class="err" role="alert">{delError}</p>{/if}
    </form>
    {#snippet footer()}
      <Button onclick={() => (delOpen = false)}>{t("common.cancel")}</Button>
      <Button
        type="submit"
        form="del-form"
        variant="danger"
        busy={delBusy}
        disabled={!!owned.length || !delPw}>{t("account.deleteConfirm")}</Button
      >
    {/snippet}
  </Sheet>

  <Sheet bind:open={pwOpen} title={t("account.pwChange")} width={400}>
    <form class="pw" id="pw-form" onsubmit={changePassword}>
      <TextField
        label={t("account.pwOld")}
        type="password"
        bind:value={pwOld}
        autocomplete="current-password"
        autofocus
      />
      <TextField
        label={t("account.pwNew")}
        type="password"
        bind:value={pwNew}
        autocomplete="new-password"
        hint={t("auth.passwordHint")}
      />
      <TextField
        label={t("account.pwRepeat")}
        type="password"
        bind:value={pwRepeat}
        autocomplete="new-password"
      />
      <div class="check">
        <span>{t("account.pwRevoke")}</span>
        <Toggle label={t("account.pwRevoke")} bind:checked={pwRevoke} />
      </div>
      {#if pwError}<p class="err" role="alert">{pwError}</p>{/if}
    </form>
    {#snippet footer()}
      <Button onclick={() => (pwOpen = false)}>{t("common.cancel")}</Button>
      <Button
        type="submit"
        form="pw-form"
        variant="primary"
        busy={pwBusy}
        disabled={!pwOld || pwNew.length < 10 || !pwRepeat}>{t("account.pwChange")}</Button
      >
    {/snippet}
  </Sheet>
{/if}

<style>
  .profile {
    gap: 14px;
    padding: 14px;
  }
  .pinfo {
    min-width: 0;
  }
  .pname {
    font-size: var(--fs-title3);
    font-weight: 600;
  }
  .spacer {
    flex: 1;
  }
  .body {
    margin: 0;
    color: var(--text-2);
  }
  .form {
    padding: 14px;
  }
  .fields {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .fields.single {
    grid-template-columns: 1fr;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 12px;
  }
  .err {
    margin: 10px 0 0;
    color: var(--danger);
    font-size: var(--fs-callout);
  }
  .badge {
    margin-left: 6px;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent);
    color: var(--on-accent);
    font-size: var(--fs-caption);
  }
  .pw {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .check {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
</style>
