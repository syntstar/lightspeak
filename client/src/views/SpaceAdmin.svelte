<script lang="ts">
  // Space administration sheets: invites, create/edit a
  // channel, space settings (general, members, bans). Loaded on first use;
  // spaceAdmin.sheet decides what is shown. Every action is authorised by
  // the server – the UI only hides what the current role can't do.
  import X from "@lucide/svelte/icons/x";
  import Copy from "@lucide/svelte/icons/copy";
  import Ellipsis from "@lucide/svelte/icons/ellipsis";
  import type { Channel, ID, User } from "../lib/api/types";
  import { errorCode } from "../lib/api/client";
  import { app, api } from "../lib/state/app.svelte";
  import { fetchServerInfo } from "../lib/server";
  import { i18n, t, tp, terr, type MessageKey } from "../lib/i18n/index.svelte";
  import {
    spaceAdmin,
    accessOf,
    accessOfMember,
    isOwnerLevel,
    memberAdminItems,
    memberLabel,
    type AdminSheet,
    type ChannelType,
    type SettingsTab,
  } from "../lib/spaceAdmin.svelte";
  import { P, has } from "../lib/perms";
  import RolesPane from "./RolesPane.svelte";
  import ChannelPermsPane from "./ChannelPermsPane.svelte";
  import {
    AUDIO_LEVELS,
    AWAY_STEPS,
    LIMIT_STEPS,
    SLOWMODE_STEPS,
    audioOf,
    modeOf,
    type AudioLevel,
  } from "../lib/channelRules";
  import type { Override } from "../lib/api/types";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import Sheet from "../lib/ui/Sheet.svelte";
  import Button from "../lib/ui/Button.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import TextField from "../lib/ui/TextField.svelte";
  import SegmentedControl from "../lib/ui/SegmentedControl.svelte";
  import Select from "../lib/ui/Select.svelte";
  import Toggle from "../lib/ui/Toggle.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import { ICON_CHOICES, channelIcon } from "../lib/channelIcons";

  interface Invite {
    code: string;
    maxUses: number | null;
    uses: number;
    expiresAt: number | null;
    url: string;
  }
  interface BanEntry {
    user: User;
    reason: string;
    createdAt: number;
  }

  const MAX_ICON = 5 * 1024 * 1024;

  let open = $state(true);
  const s = $derived(spaceAdmin.sheet);
  const space = $derived(s ? app.spaces[s.spaceId] : undefined);
  const acc = $derived(s ? accessOf(s.spaceId) : null);
  const canSpace = $derived(!!acc && has(acc, P.manageSpace));
  const ownerLevel = $derived(!!s && isOwnerLevel(s.spaceId));
  const categories = $derived(s ? app.spaceChannels(s.spaceId).filter((c) => c.kind === "category") : []);

  // The sheet closes itself if the space disappears (deleted, kicked).
  $effect(() => {
    if (s && !space) spaceAdmin.close();
  });

  function fail(err: unknown) {
    toast(terr(errorCode(err)), "error");
  }
  function onclose() {
    spaceAdmin.close();
  }
  function done() {
    open = false;
    spaceAdmin.close();
  }
  const fmtDate = (ms: number) =>
    new Intl.DateTimeFormat(i18n.locale, { dateStyle: "medium", timeStyle: "short" }).format(ms);

  // ---------- invites ----------
  let maxAge = $state("86400");
  let maxUses = $state("0");
  let created = $state<Invite | null>(null);
  let invites = $state<Invite[]>([]);
  let inviteBusy = $state(false);
  let signupHint = $state(false);
  const inviteUrl = (i: Invite) => i.url || `${api.base}/invite/${i.code}`;

  async function loadInvites(spaceId: ID) {
    try {
      invites = await api.get<Invite[]>(`/spaces/${spaceId}/invites`);
    } catch (err) {
      fail(err);
    }
  }
  async function checkSignup() {
    // New accounts need a server admin's invite unless the server says otherwise.
    if (app.me?.isServerAdmin) return;
    const info = await fetchServerInfo(api.base).catch(() => null);
    signupHint = !!info && info.signup !== "invite";
  }
  async function createInvite(spaceId: ID) {
    inviteBusy = true;
    try {
      created = await api.post<Invite>(`/spaces/${spaceId}/invites`, {
        maxAgeSeconds: Number(maxAge),
        maxUses: Number(maxUses),
      });
      void loadInvites(spaceId);
      await copy(inviteUrl(created));
    } catch (err) {
      fail(err);
    } finally {
      inviteBusy = false;
    }
  }
  async function revoke(spaceId: ID, code: string) {
    try {
      await api.del(`/invites/${encodeURIComponent(code)}`);
      if (created?.code === code) created = null;
      toast(t("invite.revoked"), "success");
      void loadInvites(spaceId);
    } catch (err) {
      fail(err);
    }
  }
  async function copy(text: string) {
    // The link stays visible and selectable if the clipboard is unavailable.
    try {
      await navigator.clipboard.writeText(text);
      toast(t("toast.copied"), "success");
    } catch {
      /* ignore */
    }
  }
  function inviteMeta(i: Invite): string {
    const expiry = i.expiresAt ? t("invite.expiresAt", { date: fmtDate(i.expiresAt) }) : t("invite.noExpiry");
    return i.maxUses
      ? t("invite.meta", { uses: i.uses, max: i.maxUses, expiry })
      : t("invite.metaUnlimited", { uses: i.uses, expiry });
  }

  // ---------- channel ----------
  // Tabs like TeamSpeak: General · Audio (voice) · Permissions.
  type ChannelTab = "general" | "audio" | "perms";
  let chTab = $state<ChannelTab>("general");
  let chType = $state<ChannelType>("text");
  let chParent = $state("");
  let chName = $state("");
  let chTopic = $state("");
  let chLimit = $state("0");
  let chAwayOn = $state(false);
  let chAway = $state("0");
  let chSlow = $state("0");
  let chAudio = $state<AudioLevel>("speech");
  let chIcon = $state("");
  let chOverrides = $state<Override[]>([]);
  let chError = $state<string | null>(null);
  let chBusy = $state(false);

  // Options, labelled for people rather than numbers.
  const secs = (n: number) => (n < 60 ? t("rules.seconds", { n }) : t("rules.minutes", { n: n / 60 }));
  const slowOptions = $derived(
    SLOWMODE_STEPS.map((n) => ({ value: String(n), label: n ? secs(n) : t("rules.off") })),
  );
  const awayOptions = $derived(
    AWAY_STEPS.map((n) => ({
      value: String(n),
      label: n ? t("rules.awayAfterN", { n }) : t("rules.awayNever"),
    })),
  );
  const limitOptions = $derived.by(() => {
    const steps: number[] = [...LIMIT_STEPS];
    const cur = Number(chLimit);
    if (!steps.includes(cur)) steps.push(cur); // a value set before the presets
    return steps
      .sort((a, b) => a - b)
      .map((n) => ({ value: String(n), label: n ? tp("rules.people", n) : t("rules.unlimited") }));
  });
  const chTabs = $derived.by(() => {
    if (chType === "category") return [];
    const out: { value: ChannelTab; label: string }[] = [{ value: "general", label: t("chedit.general") }];
    if (chType === "voice") out.push({ value: "audio", label: t("chedit.audio") });
    // The away channel has fixed rules: everyone enters, nobody talks.
    if (acc && has(acc, P.manageRoles) && !(chType === "voice" && chAwayOn))
      out.push({ value: "perms", label: t("chedit.perms") });
    return out;
  });
  // Switching between text and voice while creating: drop what no longer fits.
  $effect(() => {
    if ((chType === "text" || chType === "voice") && !ICON_CHOICES[chType].includes(chIcon)) chIcon = "";
  });
  $effect(() => {
    if (!chTabs.some((x) => x.value === chTab)) chTab = "general";
  });
  let overridesKind: ChannelType | null = null;
  $effect(() => {
    if (overridesKind && overridesKind !== chType) chOverrides = [];
    overridesKind = chType;
  });

  function rules(kind: "text" | "voice") {
    // Overrides of roles deleted meanwhile are gone on the server already.
    const known = (id: ID) => id === s?.spaceId || !!space?.roles.some((r) => r.id === id);
    const overrides = chOverrides
      .filter((o) => known(o.roleId))
      .map((o) => ({ roleId: o.roleId, allow: o.allow, deny: o.deny }));
    if (kind === "text") return { slowmode: Number(chSlow), icon: chIcon, overrides };
    return {
      mode: chAwayOn ? "away" : "open",
      awayAfter: chAwayOn ? Number(chAway) : 0,
      audio: chAudio,
      icon: chIcon,
      overrides,
    };
  }

  async function saveChannel(sheet: Extract<AdminSheet, { kind: "channel" }>, e: SubmitEvent) {
    e.preventDefault();
    const limit = Number(chLimit || "0");
    chBusy = true;
    chError = null;
    try {
      const cat = chType === "category";
      const kind = sheet.channel ? sheet.channel.kind : chType;
      const extra = kind === "text" || kind === "voice" ? rules(kind) : {};
      if (sheet.channel) {
        await api.patch(`/channels/${sheet.channel.id}`, {
          name: chName.trim(),
          ...(cat ? {} : { topic: chTopic.trim(), parentId: chParent || "0" }),
          ...(sheet.channel.kind === "voice" ? { userLimit: limit } : {}),
          ...extra,
        });
      } else {
        const c = await api.post<Channel>(`/spaces/${sheet.spaceId}/channels`, {
          kind: chType,
          name: chName.trim(),
          topic: cat ? "" : chTopic.trim(),
          userLimit: chType === "voice" ? limit : 0,
          ...(!cat && chParent ? { parentId: chParent } : {}),
          ...extra,
        });
        if (c.kind === "text") app.selectChannel(c.id);
      }
      done();
    } catch (err) {
      chError = terr(errorCode(err));
    } finally {
      chBusy = false;
    }
  }

  // ---------- settings ----------
  let tab = $state<SettingsTab>("members");
  let spName = $state("");
  let nameBusy = $state(false);
  let iconBusy = $state(false);
  let confirmName = $state("");
  let deleteBusy = $state(false);
  let bans = $state<BanEntry[]>([]);

  async function saveName(spaceId: ID, e: SubmitEvent) {
    e.preventDefault();
    nameBusy = true;
    try {
      await api.patch(`/spaces/${spaceId}`, { name: spName.trim() });
      toast(t("spaceSettings.saved"), "success");
    } catch (err) {
      fail(err);
    } finally {
      nameBusy = false;
    }
  }
  async function pickIcon(spaceId: ID, e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    if (file.size > MAX_ICON) {
      toast(t("account.avatarTooLarge"), "error");
      return;
    }
    iconBusy = true;
    try {
      await api.request(`/spaces/${spaceId}/icon`, {
        method: "POST",
        raw: file,
        headers: { "Content-Type": file.type || "application/octet-stream" },
      });
    } catch (err) {
      const code = errorCode(err);
      toast(code === "invalid_image" ? t("account.avatarInvalid") : terr(code), "error");
    } finally {
      iconBusy = false;
    }
  }
  async function removeIcon(spaceId: ID) {
    iconBusy = true;
    try {
      await api.del(`/spaces/${spaceId}/icon`);
    } catch (err) {
      fail(err);
    } finally {
      iconBusy = false;
    }
  }
  async function deleteSpace(spaceId: ID, e: SubmitEvent) {
    e.preventDefault();
    deleteBusy = true;
    try {
      await api.request(`/spaces/${spaceId}`, { method: "DELETE", body: { confirmName } });
      done();
    } catch (err) {
      fail(err);
    } finally {
      deleteBusy = false;
    }
  }
  async function loadBans(spaceId: ID) {
    try {
      bans = await api.get<BanEntry[]>(`/spaces/${spaceId}/bans`);
    } catch (err) {
      fail(err);
    }
  }
  async function unban(spaceId: ID, userId: ID) {
    try {
      await api.del(`/spaces/${spaceId}/bans/${userId}`);
      bans = bans.filter((b) => b.user.id !== userId);
    } catch (err) {
      fail(err);
    }
  }
  const members = $derived(
    s
      ? [...(app.members[s.spaceId] ?? [])].sort(
          (a, b) =>
            accessOfMember(s.spaceId, b.userId).rank - accessOfMember(s.spaceId, a.userId).rank ||
            (app.user(a.userId)?.displayName ?? "").localeCompare(app.user(b.userId)?.displayName ?? ""),
        )
      : [],
  );
  const tabs = $derived.by(() => {
    if (!s || !acc) return [];
    const out: { value: SettingsTab; label: string }[] = [];
    if (canSpace || ownerLevel) out.push({ value: "general", label: t("spaceSettings.general") });
    if (has(acc, P.manageRoles)) out.push({ value: "roles", label: t("roles.title") });
    out.push({ value: "members", label: t("spaceSettings.members") });
    if (has(acc, P.ban)) out.push({ value: "bans", label: t("spaceSettings.bans") });
    return out;
  });

  // Initialise the fields whenever another sheet is opened.
  let initFor: AdminSheet | null = null;
  $effect(() => {
    const cur = s;
    if (!cur || cur === initFor) return;
    initFor = cur;
    open = true;
    if (cur.kind === "invite") {
      created = null;
      maxAge = "86400";
      maxUses = "0";
      void loadInvites(cur.spaceId);
      void checkSignup();
    } else if (cur.kind === "channel") {
      chTab = "general";
      chType = cur.type;
      overridesKind = cur.type;
      chParent = cur.channel?.parentId ?? cur.parentId ?? "";
      chName = cur.channel?.name ?? "";
      chTopic = cur.channel?.topic ?? "";
      chLimit = String(cur.channel?.userLimit ?? 0);
      chAwayOn = !!cur.channel && modeOf(cur.channel) === "away";
      chAway = String(cur.channel?.awayAfter ?? 0);
      chSlow = String(cur.channel?.slowmode ?? 0);
      chAudio = audioOf(cur.channel ?? undefined);
      chIcon = cur.channel?.icon ?? "";
      chOverrides = (cur.channel?.overrides ?? []).map((o) => ({ ...o }));
      chError = null;
    } else {
      tab = cur.tab;
      spName = app.spaces[cur.spaceId]?.name ?? "";
      confirmName = "";
      void app.loadMembers(cur.spaceId, true);
    }
  });
  $effect(() => {
    if (s?.kind === "settings" && tab === "bans") void loadBans(s.spaceId);
  });

  const title = $derived.by(() => {
    if (!s) return "";
    if (s.kind === "invite") return t("invite.titleIn", { name: space?.name ?? "" });
    if (s.kind === "channel") {
      if (s.type === "category") return s.channel ? t("channel.editCategory") : t("channel.createCategory");
      return s.channel ? t("channel.edit") : t("channel.create");
    }
    return t("spaceSettings.title");
  });
</script>

{#if s && space}
  <Sheet
    bind:open
    {title}
    width={s.kind === "settings" || (s.kind === "channel" && chTabs.length > 1) ? 560 : 480}
    {onclose}
  >
    {#if s.kind === "invite"}
      <p class="sub">{t("invite.sub")}</p>
      {#if signupHint}<p class="hint">{t("invite.signupHint")}</p>{/if}
      <div class="field">
        <span class="lbl" aria-hidden="true">{t("invite.expires")}</span>
        <SegmentedControl
          label={t("invite.expires")}
          bind:value={maxAge}
          options={[
            { value: "3600", label: t("invite.h1") },
            { value: "86400", label: t("invite.d1") },
            { value: "604800", label: t("invite.d7") },
            { value: "0", label: t("invite.never") },
          ]}
        />
      </div>
      <div class="field">
        <span class="lbl" aria-hidden="true">{t("invite.uses")}</span>
        <Select
          label={t("invite.uses")}
          bind:value={maxUses}
          options={[
            { value: "0", label: t("invite.usesUnlimited") },
            ...[1, 5, 10, 25, 100].map((n) => ({ value: String(n), label: tp("invite.people", n) })),
          ]}
        />
      </div>
      {#if created}
        <div class="link">
          <input
            class="selectable"
            readonly
            value={inviteUrl(created)}
            aria-label={t("invite.link")}
            onfocus={(e) => e.currentTarget.select()}
          />
          <Button onclick={() => void copy(inviteUrl(created!))}
            ><Copy size={14} strokeWidth={1.75} />{t("invite.copy")}</Button
          >
        </div>
      {/if}
      <h3 class="gh">{t("invite.active")}</h3>
      <div class="list">
        {#each invites as i (i.code)}
          <div class="item">
            <span class="two">
              <span class="mono selectable">{i.code}</span>
              <small>{inviteMeta(i)}</small>
            </span>
            <IconButton label={t("invite.copy")} onclick={() => void copy(inviteUrl(i))}
              ><Copy size={15} strokeWidth={1.75} /></IconButton
            >
            <IconButton label={t("invite.revoke")} onclick={() => void revoke(s.spaceId, i.code)}
              ><X size={16} strokeWidth={1.75} /></IconButton
            >
          </div>
        {:else}
          <p class="empty">{t("invite.none")}</p>
        {/each}
      </div>
    {:else if s.kind === "channel"}
      {#if chTabs.length > 1}
        <div class="tabs">
          <SegmentedControl label={t("chedit.tabs")} bind:value={chTab} options={chTabs} />
        </div>
      {/if}
      <form id="channel-form" class="stack" onsubmit={(e) => void saveChannel(s, e)}>
        {#if chTab === "general"}
          {#if !s.channel && s.type !== "category"}
            <div class="field">
              <span class="lbl" aria-hidden="true">{t("channel.type")}</span>
              <SegmentedControl
                label={t("channel.type")}
                bind:value={chType}
                options={[
                  { value: "text" as const, label: t("channel.typeText") },
                  { value: "voice" as const, label: t("channel.typeVoice") },
                ]}
              />
            </div>
          {/if}
          <TextField label={t("channel.name")} bind:value={chName} maxlength={64} error={chError} autofocus />
          {#if chType !== "category"}
            <TextField
              label={t("channel.topic")}
              hint={t("channel.topicHint")}
              bind:value={chTopic}
              maxlength={256}
            />
            {#if categories.length}
              <div class="field">
                <span class="lbl" aria-hidden="true">{t("channel.category")}</span>
                <Select
                  label={t("channel.category")}
                  bind:value={chParent}
                  options={[
                    { value: "", label: t("channel.noCategory") },
                    ...categories.map((c) => ({ value: c.id, label: c.name })),
                  ]}
                />
              </div>
            {/if}
          {/if}
          {#if chType === "text" || (chType === "voice" && !chAwayOn)}
            {@const kind = chType}
            <div class="field">
              <span class="lbl" id="ch-icon-lbl">{t("channel.icon")}</span>
              <div class="icons" role="radiogroup" aria-labelledby="ch-icon-lbl">
                {#each ICON_CHOICES[kind] as key (key)}
                  {@const Icon = channelIcon(kind, key)}
                  {@const name = key ? t(`icon.${key}` as MessageKey) : t("channel.iconDefault")}
                  <label class="icon-opt" title={name}>
                    <input type="radio" name="ch-icon" value={key} bind:group={chIcon} aria-label={name} />
                    <Icon size={18} strokeWidth={1.75} />
                  </label>
                {/each}
              </div>
            </div>
          {/if}
          {#if chType === "text"}
            <div class="field">
              <span class="lbl" aria-hidden="true">{t("rules.slowmode")}</span>
              <Select label={t("rules.slowmode")} bind:value={chSlow} options={slowOptions} />
              <small class="fhint">{t("rules.slowmodeHint")}</small>
            </div>
          {:else if chType === "voice"}
            <div class="field">
              <span class="lbl" aria-hidden="true">{t("channel.userLimit")}</span>
              <Select label={t("channel.userLimit")} bind:value={chLimit} options={limitOptions} />
            </div>
            <div class="group">
              <div class="grow">
                <span class="two">
                  {t("chedit.away")}
                  <small>{t("chedit.awayHint")}</small>
                </span>
                <Toggle label={t("chedit.away")} bind:checked={chAwayOn} />
              </div>
              {#if chAwayOn}
                <div class="grow">
                  <span class="two">
                    {t("rules.awayAfter")}
                    <small>{t("rules.awayHint")}</small>
                  </span>
                  <Select label={t("rules.awayAfter")} bind:value={chAway} options={awayOptions} />
                </div>
              {/if}
            </div>
          {/if}
        {:else if chTab === "audio"}
          <div class="group" role="radiogroup" aria-label={t("chedit.audioQuality")}>
            {#each AUDIO_LEVELS as lvl (lvl)}
              <label class="grow choice">
                <input type="radio" name="audio" value={lvl} bind:group={chAudio} />
                <span class="two">
                  {t(`chedit.audio.${lvl}` as MessageKey)}
                  <small>{t(`chedit.audio.${lvl}.hint` as MessageKey)}</small>
                </span>
              </label>
            {/each}
          </div>
          <small class="fhint">{t("chedit.audioHint")}</small>
        {:else if chTab === "perms" && (chType === "text" || chType === "voice")}
          <ChannelPermsPane spaceId={s.spaceId} kind={chType} bind:value={chOverrides} />
        {/if}
      </form>
    {:else}
      <div class="field">
        <SegmentedControl label={t("spaceSettings.title")} bind:value={tab} options={tabs} />
      </div>
      {#if tab === "general" && (canSpace || ownerLevel)}
        <form class="row-form" onsubmit={(e) => void saveName(s.spaceId, e)}>
          <TextField label={t("space.name")} bind:value={spName} maxlength={64} />
          <Button type="submit" busy={nameBusy} disabled={!spName.trim() || spName.trim() === space.name}
            >{t("common.save")}</Button
          >
        </form>
        <div class="icon-row">
          <Avatar name={space.name} id={space.id} src={space.iconUrl && api.url(space.iconUrl)} size={48} />
          <label class="btn-like" class:busy={iconBusy}>
            {t("spaceSettings.iconChange")}
            <input
              type="file"
              accept="image/png,image/jpeg,image/gif"
              onchange={(e) => void pickIcon(s.spaceId, e)}
            />
          </label>
          {#if space.iconUrl}
            <Button variant="plain" busy={iconBusy} onclick={() => void removeIcon(s.spaceId)}
              >{t("spaceSettings.iconRemove")}</Button
            >
          {/if}
        </div>
        {#if ownerLevel}
          <h3 class="gh">{t("spaceSettings.owner")}</h3>
          <p class="hint">
            {t("spaceSettings.ownerHint", { name: app.user(space.ownerId)?.displayName ?? "?" })}
          </p>
          <h3 class="gh danger">{t("spaceSettings.delete")}</h3>
          <form class="danger-zone" onsubmit={(e) => void deleteSpace(s.spaceId, e)}>
            <p class="hint">{t("spaceSettings.deleteHint", { name: space.name })}</p>
            <TextField label={t("spaceSettings.deleteName")} bind:value={confirmName} maxlength={64} />
            <Button variant="danger" type="submit" busy={deleteBusy} disabled={confirmName !== space.name}
              >{t("spaceSettings.delete")}</Button
            >
          </form>
        {/if}
      {:else if tab === "roles"}
        <RolesPane spaceId={s.spaceId} />
      {:else if tab === "members"}
        <div class="list">
          {#each members as m (m.userId)}
            {@const u = app.user(m.userId)}
            {@const items = memberAdminItems(s.spaceId, m.userId)}
            <div class="item">
              <Avatar
                name={u?.displayName ?? "?"}
                id={m.userId}
                src={u?.avatarUrl && api.url(u.avatarUrl)}
                size={28}
              />
              <span class="two">
                {u?.displayName ?? "?"}
                <small
                  >{memberLabel(s.spaceId, m.userId, m.roleIds ?? [], !!m.serverAdmin) ||
                    t("members.member")}</small
                >
              </span>
              {#if items.length}
                <IconButton
                  label={t("spaceSettings.memberActions", { name: u?.displayName ?? "" })}
                  onclick={(e: MouseEvent) =>
                    openMenu(
                      e,
                      items.filter((x) => !("separator" in x)),
                    )}><Ellipsis size={16} strokeWidth={1.75} /></IconButton
                >
              {/if}
            </div>
          {/each}
        </div>
      {:else if tab === "bans"}
        <div class="list">
          {#each bans as b (b.user.id)}
            <div class="item">
              <Avatar
                name={b.user.displayName}
                id={b.user.id}
                src={b.user.avatarUrl && api.url(b.user.avatarUrl)}
                size={28}
              />
              <span class="two">
                {b.user.displayName}
                <small>{fmtDate(b.createdAt)}</small>
              </span>
              <Button variant="plain" onclick={() => void unban(s.spaceId, b.user.id)}
                >{t("spaceSettings.unban")}</Button
              >
            </div>
          {:else}
            <p class="empty">{t("spaceSettings.noBans")}</p>
          {/each}
        </div>
      {/if}
    {/if}
    {#snippet footer()}
      {#if s.kind === "channel"}
        <Button onclick={done}>{t("common.cancel")}</Button>
        <Button variant="primary" type="submit" form="channel-form" busy={chBusy} disabled={!chName.trim()}
          >{s.channel ? t("common.save") : t("common.create")}</Button
        >
      {:else if s.kind === "invite"}
        <Button onclick={done}>{t("common.close")}</Button>
        <Button variant="primary" busy={inviteBusy} onclick={() => void createInvite(s.spaceId)}
          >{created ? t("invite.createAnother") : t("invite.create")}</Button
        >
      {:else}
        <Button variant="primary" onclick={done}>{t("common.done")}</Button>
      {/if}
    {/snippet}
  </Sheet>
{/if}

<style>
  .sub,
  .hint {
    margin: 0;
    color: var(--text-2);
  }
  .hint {
    font-size: var(--fs-small);
  }
  .field {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
  }
  .fhint {
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .lbl {
    font-size: var(--fs-callout);
    font-weight: 600;
    color: var(--text-2);
  }
  .link {
    display: flex;
    gap: 8px;
    margin: 4px 0 12px;
  }
  .link input {
    flex: 1;
    min-width: 0;
    height: 30px;
    padding: 0 10px;
    border: 1px solid var(--border-hairline);
    border-radius: var(--r-sm);
    background: var(--bg-input);
    color: var(--text-1);
    font: inherit;
  }
  .gh {
    margin: 4px 0 -8px;
    font-size: var(--fs-small);
    font-weight: 600;
    color: var(--text-2);
  }
  .gh.danger {
    color: var(--danger);
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 40px;
    padding: 4px 6px;
    border-radius: var(--r-sm);
  }
  .item:hover {
    background: var(--bg-hover);
  }
  .two {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .two small {
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .mono {
    font-family: var(--font-mono);
  }
  .empty {
    margin: 4px 6px;
    color: var(--text-3);
  }
  .stack {
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
  }
  .tabs {
    display: flex;
    justify-content: center;
    margin-bottom: var(--sp-4);
  }
  /* Grouped rows, like the inset lists of macOS settings. */
  .group {
    display: flex;
    flex-direction: column;
    border-radius: var(--r-lg);
    background: var(--bg-input);
  }
  .grow {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    min-height: 48px;
    padding: 8px 12px;
  }
  .grow + .grow {
    border-top: 1px solid var(--separator);
  }
  .choice {
    cursor: pointer;
  }
  /* Icon picker: a grid of icon buttons, radios underneath for
     keyboard (arrow keys) and screen readers. */
  .icons {
    display: grid;
    grid-template-columns: repeat(auto-fill, 36px);
    gap: 4px;
    width: 100%;
  }
  .icon-opt {
    position: relative;
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border-radius: var(--r-sm);
    color: var(--text-2);
    cursor: pointer;
  }
  .icon-opt:hover {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .icon-opt input {
    position: absolute;
    inset: 0;
    margin: 0;
    opacity: 0;
    cursor: pointer;
  }
  .icon-opt:has(input:checked) {
    background: color-mix(in srgb, var(--accent) 18%, transparent);
    color: var(--accent);
    box-shadow: inset 0 0 0 1.5px var(--accent);
  }
  .icon-opt:has(input:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .choice input {
    flex: none;
    margin: 0;
    accent-color: var(--accent);
  }
  .row-form {
    display: flex;
    gap: 8px;
    align-items: flex-end;
  }
  .row-form :global(.field),
  .row-form :global(label) {
    flex: 1;
  }
  .icon-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 14px 0;
  }
  .btn-like {
    position: relative;
    display: inline-flex;
    align-items: center;
    height: 28px;
    padding: 0 12px;
    border-radius: var(--r-sm);
    background: var(--bg-active);
    color: var(--text-1);
    cursor: pointer;
  }
  .btn-like:focus-within {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .btn-like.busy {
    opacity: 0.6;
    pointer-events: none;
  }
  .btn-like input {
    position: absolute;
    inset: 0;
    opacity: 0;
    cursor: pointer;
  }
  .danger-zone {
    display: flex;
    flex-direction: column;
    gap: 8px;
    align-items: flex-start;
  }
  .danger-zone :global(label),
  .danger-zone :global(.field) {
    align-self: stretch;
  }
</style>
