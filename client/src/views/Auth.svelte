<script lang="ts">
  // Server choice, login and invite-based registration. The app has no
  // built-in server: the user picks one by address or invite link,
  // and a link to a different server is always shown and confirmed first.
  import { onMount } from "svelte";
  import Globe from "@lucide/svelte/icons/globe";
  import { api, app } from "../lib/state/app.svelte";
  import { ApiError, errorCode } from "../lib/api/client";
  import type { InvitePreview } from "../lib/api/types";
  import { native, hasStore, isWeb } from "../lib/native";
  import { prefs } from "../lib/prefs.svelte";
  import {
    fetchServerInfo,
    inviteLink,
    isInsecureAddress,
    normalizeServer,
    parseInvite,
    serverHost,
    type ServerInfo,
  } from "../lib/server";
  import { t, terr, tp, type MessageKey } from "../lib/i18n/index.svelte";
  import Button from "../lib/ui/Button.svelte";
  import TextField from "../lib/ui/TextField.svelte";
  import CaptionButtons from "./CaptionButtons.svelte";

  let mode = $state<"login" | "register">("login");
  let username = $state("");
  let displayName = $state("");
  let password = $state("");
  let password2 = $state("");
  let inviteInput = $state("");
  let preview = $state<InvitePreview | null>(null);
  let previewError = $state<string | null>(null);
  let error = $state<string | null>(app.authNotice ? t(app.authNotice as MessageKey) : null);
  let busy = $state(false);
  let firstField = $state<HTMLInputElement>();

  // ---------- server picker ----------
  let serverInput = $state(import.meta.env.DEV && !prefs.server ? "127.0.0.1:8080" : "");
  let serverError = $state<string | null>(null);
  /** Result of looking up the address in the field (shown as a card). */
  let lookup = $state<{ origin: string; info: ServerInfo } | null>(null);

  const invite = $derived(parseInvite(inviteInput));
  const inviteCode = $derived(
    invite && (!invite.server || invite.server === app.server) ? invite.code : null,
  );
  const inviteElsewhere = $derived(invite?.server && invite.server !== app.server ? invite.server : null);
  const serverName = $derived(prefs.servers[app.server]?.name || serverHost(app.server));
  const known = $derived(
    Object.entries(prefs.servers)
      .map(([origin, sp]) => ({ origin, name: sp.name || serverHost(origin) }))
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  onMount(() => firstField?.focus());

  // Browser client: E2EE needs WebCrypto X25519 and encoded media streams.
  let unsupported = $state(false);
  onMount(() => {
    if (isWeb) void native.browserSupported().then((ok) => (unsupported = !ok));
  });

  // A deep link can arrive while this screen is open (or started the app).
  $effect(() => {
    const link = app.pendingInvite;
    if (!link) return;
    app.pendingInvite = null;
    const inv = parseInvite(link);
    if (!inv) return;
    if (inv.server && inv.server !== app.server) {
      // Different (or no) server: show it and let the user confirm.
      app.pickServer = true;
      serverInput = link;
      void look(inv.server);
    } else {
      app.pickServer = false;
      inviteInput = link;
      switchMode("register");
    }
  });

  /** Address typed or pasted into the picker → server origin (or error). */
  function originOf(input: string): string | null {
    const inv = parseInvite(input);
    if (inv?.server) return inv.server;
    return normalizeServer(input);
  }

  async function look(origin: string): Promise<ServerInfo | null> {
    serverError = null;
    busy = true;
    try {
      const info = await fetchServerInfo(origin);
      lookup = { origin, info };
      return info;
    } catch (e) {
      lookup = null;
      const code = errorCode(e);
      serverError = code === "network" ? t("server.unreachable") : terr(code);
      return null;
    } finally {
      busy = false;
    }
  }

  async function chooseServer(e?: SubmitEvent) {
    e?.preventDefault();
    const origin = originOf(serverInput);
    if (!origin) {
      serverError = isInsecureAddress(serverInput) ? t("server.insecure") : t("server.invalid");
      return;
    }
    const info = lookup?.origin === origin ? lookup.info : await look(origin);
    if (!info) return;
    await enter(origin, info.name, parseInvite(serverInput));
  }

  async function enter(origin: string, name: string | undefined, inv: ReturnType<typeof parseInvite>) {
    app.useServer(origin, name);
    lookup = null;
    serverError = null;
    if (inv?.server === origin) {
      // Coming from an invite: register there (or join with the saved session).
      if (await app.resume(origin)) {
        app.pendingInvite = inviteLink(origin, inv.code);
        return;
      }
      inviteInput = inviteLink(origin, inv.code);
      app.pickServer = false;
      switchMode("register");
      return;
    }
    if (await app.resume(origin)) return;
    app.pickServer = false;
    switchMode("login");
  }

  async function chooseKnown(origin: string) {
    if (await app.resume(origin)) return;
    app.pickServer = false;
    switchMode("login");
    // Refresh the cached name in the background; errors show on submit.
    void fetchServerInfo(origin)
      .then((info) => app.useServer(origin, info.name))
      .catch(() => {});
  }

  function changeServer() {
    serverInput = "";
    lookup = null;
    serverError = null;
    error = null;
    app.pickServer = true;
    queueMicrotask(() => firstField?.focus());
  }

  // ---------- account ----------

  // Invite preview (debounced).
  $effect(() => {
    const code = inviteCode;
    preview = null;
    previewError = null;
    if (mode !== "register" || !code || app.pickServer) return;
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      api
        .request<InvitePreview>(`/invites/${code}`, { signal: ctl.signal })
        .then((p) => {
          preview = p;
          if (p.signup === false) previewError = terr("signup_restricted");
        })
        .catch((e: unknown) => {
          if (e instanceof ApiError) previewError = terr(e.code);
        });
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  });

  function deviceName(): string {
    const pc = app.info.computerName || "Windows";
    return app.info.profile === "default" ? pc : `${pc} (${app.info.profile})`;
  }

  async function devicePublicKey(): Promise<string> {
    if (!hasStore) throw new ApiError("device_key_missing", 0);
    const k = await native.deviceKey(app.server, true);
    if (!k) throw new ApiError("device_key_missing", 0);
    return k;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    if (mode === "register") {
      if (!inviteCode) {
        error = terr("invite_invalid");
        return;
      }
      if (password !== password2) {
        error = t("auth.passwordMismatch");
        return;
      }
    }
    busy = true;
    try {
      const pub = await devicePublicKey();
      const uname = username.trim().toLowerCase();
      if (mode === "login") {
        const dev = prefs.servers[app.server]?.devices[uname];
        const res = await api.login({
          username: uname,
          password,
          deviceName: deviceName(),
          devicePublicKey: pub,
          deviceId: dev && dev.publicKey === pub ? dev.deviceId : undefined,
        });
        await app.completeAuth(res.token, res.user, res.device.id, pub);
      } else {
        const res = await api.register({
          inviteCode: inviteCode!,
          username: uname,
          displayName: displayName.trim(),
          password,
          deviceName: deviceName(),
          devicePublicKey: pub,
        });
        await app.completeAuth(res.token, res.user, res.device.id, pub);
      }
    } catch (err) {
      const code = errorCode(err);
      error = code === "network" ? t("auth.offline") : terr(code);
      password = mode === "login" ? "" : password;
    } finally {
      busy = false;
    }
  }

  function switchMode(m: "login" | "register") {
    mode = m;
    error = null;
    queueMicrotask(() => firstField?.focus());
  }

  /** Invite for another server: hand it to the picker, which shows that
   *  server and waits for confirmation. */
  function followInvite() {
    app.pendingInvite = inviteInput;
    inviteInput = "";
  }
</script>

<div class="auth">
  <header data-tauri-drag-region>
    <CaptionButtons />
  </header>
  <main>
    {#snippet logo()}
      <div class="logo" aria-hidden="true">
        <svg viewBox="0 0 48 48" width="48" height="48">
          <defs>
            <linearGradient id="lvg" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0" stop-color="#0A84FF" />
              <stop offset="1" stop-color="#BF5AF2" />
            </linearGradient>
          </defs>
          <rect width="48" height="48" rx="13" fill="url(#lvg)" />
          <path
            d="M14 26v-4M19 30V18M24 33V15M29 30V18M34 26v-4"
            stroke="#fff"
            stroke-width="3"
            stroke-linecap="round"
          />
        </svg>
      </div>
    {/snippet}

    {#if app.pickServer}
      <form class="card" onsubmit={chooseServer} novalidate>
        {@render logo()}
        <h1>{t("auth.welcome")}</h1>
        <p class="sub">{t("server.sub")}</p>
        <TextField
          label={t("server.address")}
          placeholder={t("server.placeholder")}
          bind:value={serverInput}
          bind:el={firstField}
          error={serverError}
          oninput={() => {
            lookup = null;
            serverError = null;
          }}
          autocomplete="url"
          spellcheck="false"
        />
        {#if lookup}
          <div class="server-card" role="status">
            <Globe size={18} strokeWidth={1.75} />
            <div>
              <strong>{lookup.info.name}</strong>
              <span class="muted">{serverHost(lookup.origin)}</span>
            </div>
          </div>
          <p class="hint">{t("server.confirmHint")}</p>
        {/if}
        <Button type="submit" variant="primary" size="lg" wide {busy} disabled={!serverInput.trim()}>
          {t("server.continue")}
        </Button>
        {#if known.length}
          <div class="known">
            <span class="lbl">{t("server.known")}</span>
            {#each known as k (k.origin)}
              <button type="button" class="known-item" onclick={() => void chooseKnown(k.origin)}>
                <Globe size={16} strokeWidth={1.75} />
                <span class="kn">{k.name}</span>
                <span class="muted">{serverHost(k.origin)}</span>
              </button>
            {/each}
          </div>
        {/if}
      </form>
    {:else}
      <form class="card" onsubmit={submit} novalidate>
        {@render logo()}
        <h1>{mode === "login" ? t("auth.welcome") : t("auth.register")}</h1>
        <p class="sub">
          {#if mode === "register" && preview}
            {preview.space ? t("auth.inviteFor", { space: preview.space.name }) : t("auth.inviteGeneric")}
            {#if preview.space}<span class="muted"
                >· {tp("auth.inviteMembers", preview.space.memberCount)}</span
              >{/if}
          {:else}
            {t("auth.welcomeSub")}
          {/if}
        </p>

        <div class="server-chip">
          <Globe size={16} strokeWidth={1.75} />
          <span class="sn" title={app.server}>{serverName}</span>
          {#if serverName !== serverHost(app.server)}<span class="muted">{serverHost(app.server)}</span>{/if}
          {#if !isWeb}
            <button type="button" class="link" onclick={changeServer}>{t("server.change")}</button>
          {/if}
        </div>

        {#if mode === "register"}
          <TextField
            label={t("auth.invite")}
            placeholder={t("auth.invitePlaceholder")}
            bind:value={inviteInput}
            bind:el={firstField}
            error={inviteInput && !invite ? terr("invite_invalid") : inviteElsewhere ? null : previewError}
            autocomplete="off"
            spellcheck="false"
          />
          {#if inviteElsewhere}
            <div class="elsewhere" role="status">
              <span>{t("server.inviteOther", { host: serverHost(inviteElsewhere) })}</span>
              {#if !isWeb}
                <Button onclick={followInvite}>
                  {t("server.switchTo", { host: serverHost(inviteElsewhere) })}
                </Button>
              {/if}
            </div>
          {/if}
        {/if}
        {#if mode === "login"}
          <TextField
            label={t("auth.username")}
            bind:value={username}
            bind:el={firstField}
            autocomplete="username"
            spellcheck="false"
            required
          />
        {:else}
          <TextField
            label={t("auth.username")}
            bind:value={username}
            hint={t("auth.usernameHint")}
            autocomplete="username"
            spellcheck="false"
            maxlength={32}
            required
          />
          <TextField
            label={t("auth.displayName")}
            bind:value={displayName}
            placeholder={username || undefined}
            maxlength={32}
          />
        {/if}
        <TextField
          label={t("auth.password")}
          type="password"
          bind:value={password}
          hint={mode === "register" ? t("auth.passwordHint") : undefined}
          autocomplete={mode === "login" ? "current-password" : "new-password"}
          required
        />
        {#if mode === "register"}
          <TextField
            label={t("auth.passwordRepeat")}
            type="password"
            bind:value={password2}
            autocomplete="new-password"
            required
          />
        {/if}

        {#if error}<p class="error" role="alert">{error}</p>{/if}

        <Button
          type="submit"
          variant="primary"
          size="lg"
          wide
          {busy}
          disabled={!username ||
            !password ||
            (mode === "register" && (!inviteCode || preview?.signup === false))}
        >
          {mode === "login" ? t("auth.login") : t("auth.register")}
        </Button>
        <Button variant="plain" wide onclick={() => switchMode(mode === "login" ? "register" : "login")}>
          {mode === "login" ? t("auth.haveInvite") : t("auth.haveAccount")}
        </Button>
        {#if isWeb}
          {#if unsupported}
            <p class="error" role="alert">{t("web.unsupported")}</p>
          {/if}
          <p class="hint web-app">
            {t("web.appHint")} <a href="/download">{t("web.appLink")}</a>
          </p>
        {/if}
      </form>
    {/if}
  </main>
</div>

<style>
  .auth {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg-content);
  }
  header {
    display: flex;
    justify-content: flex-end;
    height: var(--titlebar-h);
    flex: none;
  }
  main {
    flex: 1;
    overflow: auto;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: 24px 24px 48px;
  }
  .card {
    width: 360px;
    margin-top: 4vh;
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
  }
  .logo {
    display: flex;
    justify-content: center;
    margin-bottom: var(--sp-1);
  }
  h1 {
    margin: 0;
    text-align: center;
    font-size: var(--fs-title1);
    font-weight: 700;
    letter-spacing: -0.01em;
  }
  .sub {
    margin: 0 0 var(--sp-3);
    text-align: center;
    color: var(--text-2);
  }
  .muted {
    color: var(--text-3);
  }
  .hint {
    margin: 0;
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--fs-callout);
  }
  .web-app {
    margin-top: var(--sp-2);
    text-align: center;
  }
  .web-app a {
    color: var(--accent-text);
  }
  .server-card,
  .server-chip,
  .elsewhere {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3);
    border: 1px solid var(--separator);
    border-radius: var(--r-md);
    color: var(--text-2);
  }
  .server-card div {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .server-card strong {
    color: var(--text-1);
  }
  .server-chip .sn {
    color: var(--text-1);
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .server-chip .muted {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--fs-caption);
  }
  .link {
    margin-left: auto;
    border: none;
    background: none;
    padding: 0;
    color: var(--accent-text);
    font-size: var(--fs-callout);
    white-space: nowrap;
  }
  .link:hover {
    text-decoration: underline;
  }
  .elsewhere {
    flex-wrap: wrap;
    justify-content: space-between;
    border-color: var(--warning, var(--separator));
    font-size: var(--fs-callout);
  }
  .known {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
    margin-top: var(--sp-3);
  }
  .known .lbl {
    font-size: var(--fs-caption);
    color: var(--text-3);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .known-item {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3);
    border: none;
    border-radius: var(--r-md);
    background: transparent;
    color: var(--text-2);
    text-align: left;
  }
  .known-item:hover {
    background: var(--bg-hover);
  }
  .known-item .kn {
    color: var(--text-1);
    font-weight: 500;
  }
  .known-item .muted {
    margin-left: auto;
    font-size: var(--fs-caption);
  }
</style>
