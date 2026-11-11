<script lang="ts">
  // Message input: grows up to 40 % height, Enter sends, Shift+Enter newline,
  // ↑ edits the last own message, @-mention autocomplete, drafts per channel,
  // file upload via picker / paste / drag & drop with progress. Pastes that
  // would exceed the message limit become a text attachment;
  // likely secrets ask before they leave the app (9.4).
  import Plus from "@lucide/svelte/icons/plus";
  import X from "@lucide/svelte/icons/x";
  import SmilePlus from "@lucide/svelte/icons/smile-plus";
  import FileIcon from "@lucide/svelte/icons/file";
  import type { Attachment, Channel, ID, User } from "../../lib/api/types";
  import { app } from "../../lib/state/app.svelte";
  import { messages, type ChatMessage } from "../../lib/state/messages.svelte";
  import { fuzzyScore } from "../../lib/state/ids";
  import { upload, type UploadHandle } from "../../lib/upload";
  import { errorCode } from "../../lib/api/client";
  import { formatBytes, t, terr } from "../../lib/i18n/index.svelte";
  import { toast } from "../../lib/ui/toast.svelte";
  import Avatar from "../../lib/ui/Avatar.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Sheet from "../../lib/ui/Sheet.svelte";
  import { findSecrets, isSecretFileName, type SecretHit } from "../../lib/secrets";
  import { charCount, MAX_SNIPPET_BYTES, snippetFile } from "../../lib/snippet";
  import { codeFence } from "../../lib/codePaste";
  import EmojiPicker from "./EmojiPicker.svelte";
  import Megaphone from "@lucide/svelte/icons/megaphone";
  import { channelRights } from "../../lib/spaceAdmin.svelte";
  import { allowedRoles } from "../../lib/channelRules";
  import { P } from "../../lib/perms";

  let {
    channel,
    replyTo = $bindable(),
    editing = $bindable(),
    onEditLast,
    onSent,
  }: {
    channel: Channel;
    replyTo: ChatMessage | null;
    editing: ChatMessage | null;
    onEditLast: () => void;
    onSent: () => void;
  } = $props();

  interface Pending {
    key: number;
    file: File;
    progress: number;
    att?: Attachment;
    error?: string;
    handle: UploadHandle;
  }

  const drafts = new Map<ID, string>();
  let text = $state("");
  let ta = $state<HTMLTextAreaElement>();
  let files = $state<Pending[]>([]);
  let dragging = $state(false);
  let emoji = $state(false);
  let mentionQuery = $state<string | null>(null);
  let mentionSel = $state(0);
  /** displayName → id for mentions inserted via autocomplete. */
  let inserted = new Map<string, ID>();
  let keySeq = 0;

  // Channel permissions: writing, files and slow mode.
  const rights = $derived(channelRights(channel));
  const authors = $derived(allowedRoles(app.spaces[channel.spaceId ?? ""], channel, P.send));
  const lastSent = new Map<ID, number>();
  let wait = $state(0);
  let waitTimer: ReturnType<typeof setInterval> | undefined;
  const slowLeft = (): number => {
    const last = lastSent.get(channel.id);
    if (!rights.slow || !last) return 0;
    return Math.max(0, Math.ceil((last + rights.slow * 1000 - Date.now()) / 1000));
  };
  /** Counts down only while a wait is running. */
  function tick() {
    clearInterval(waitTimer);
    wait = slowLeft();
    if (wait > 0)
      waitTimer = setInterval(() => {
        wait = slowLeft();
        if (!wait) clearInterval(waitTimer);
      }, 1000);
  }
  $effect(() => {
    void channel.id;
    void rights.slow;
    tick();
    return () => clearInterval(waitTimer);
  });
  const slowText = (n: number) => (n < 60 ? t("rules.seconds", { n }) : t("rules.minutes", { n: n / 60 }));

  // "This looks like a secret" confirmation (go = send anyway).
  let secretOpen = $state(false);
  let secretAsk = $state<{
    hits: SecretHit[];
    files: string[];
    go: () => void;
    cancel?: () => void;
  } | null>(null);

  function confirmSecrets(hits: SecretHit[], fileNames: string[], go: () => void, cancel?: () => void) {
    if (!hits.length && !fileNames.length) {
      go();
      return;
    }
    secretAsk = { hits, files: fileNames, go, cancel };
    secretOpen = true;
  }

  function secretDecide(send: boolean) {
    const a = secretAsk;
    secretAsk = null;
    secretOpen = false;
    if (send) a?.go();
    else a?.cancel?.();
    queueMicrotask(() => ta?.focus());
  }
  let prevChannel: ID | null = null;

  // Drafts survive channel switches.
  $effect(() => {
    const id = channel.id;
    if (prevChannel && prevChannel !== id) drafts.set(prevChannel, text);
    prevChannel = id;
    text = drafts.get(id) ?? "";
    files = [];
    inserted = new Map();
    queueMicrotask(() => {
      ta?.focus();
      autosize();
    });
  });

  // Editing: load the message text.
  $effect(() => {
    if (editing) {
      text = editing.content;
      queueMicrotask(() => {
        ta?.focus();
        autosize();
      });
    }
  });

  function autosize() {
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, window.innerHeight * 0.4)}px`;
  }

  const candidates = $derived.by((): User[] => {
    if (mentionQuery === null) return [];
    const pool = new Map<ID, User>();
    if (channel.spaceId)
      for (const m of app.members[channel.spaceId] ?? []) pool.set(m.userId, app.user(m.userId) ?? m.user);
    for (const id of channel.memberIds ?? []) {
      const u = app.user(id);
      if (u) pool.set(id, u);
    }
    pool.delete(app.me?.id ?? "");
    return [...pool.values()]
      .map((u) => ({
        u,
        s: Math.max(fuzzyScore(mentionQuery!, u.displayName), fuzzyScore(mentionQuery!, u.username)),
      }))
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .slice(0, 8)
      .map((x) => x.u);
  });

  function updateMention() {
    if (!ta) return;
    const before = text.slice(0, ta.selectionStart);
    const m = /(^|\s)@([^\s@]{0,32})$/.exec(before);
    mentionQuery = m ? m[2]! : null;
    mentionSel = 0;
  }

  function pickMention(u: User) {
    if (!ta) return;
    const pos = ta.selectionStart;
    const before = text.slice(0, pos).replace(/@([^\s@]{0,32})$/, `@${u.displayName} `);
    text = before + text.slice(pos);
    inserted.set(u.displayName, u.id);
    mentionQuery = null;
    queueMicrotask(() => {
      ta?.setSelectionRange(before.length, before.length);
      ta?.focus();
    });
  }

  /** Converts "@Name" inserted via autocomplete into the wire form <@id>. */
  function encodeMentions(s: string): string {
    let out = s;
    for (const [name, id] of [...inserted].sort((a, b) => b[0].length - a[0].length)) {
      out = out.split(`@${name}`).join(`<@${id}>`);
    }
    return out;
  }

  function decodeMentions(s: string): string {
    return s.replace(/<@(\d{1,20})>/g, (m, id: string) => {
      const u = app.user(id);
      if (!u) return m;
      inserted.set(u.displayName, id);
      return `@${u.displayName}`;
    });
  }

  $effect(() => {
    if (editing) text = decodeMentions(editing.content);
  });

  async function submit(secretsChecked = false) {
    const content = encodeMentions(text.trim());
    if (charCount(content) > app.limits.messageChars) {
      toast(terr("message_too_long"), "error");
      return;
    }
    if (!secretsChecked && content) {
      const hits = findSecrets(content);
      if (hits.length) {
        confirmSecrets(hits, [], () => void submit(true));
        return;
      }
    }
    if (editing) {
      const m = editing;
      editing = null;
      text = drafts.get(channel.id) ?? "";
      if (content && content !== m.content) {
        try {
          await messages.edit(m, content);
        } catch (e) {
          toast(terr(errorCode(e)), "error");
        }
      }
      return;
    }
    if (files.some((f) => !f.att && !f.error)) return; // still uploading
    const atts = files.filter((f) => f.att).map((f) => f.att!);
    if (!content && !atts.length) return;
    if (slowLeft() > 0) {
      tick();
      return; // the hint above the box shows the countdown
    }
    if (rights.slow) {
      lastSent.set(channel.id, Date.now());
      tick();
    }
    const reply = replyTo ?? undefined;
    text = "";
    files = [];
    replyTo = null;
    inserted = new Map();
    drafts.delete(channel.id);
    queueMicrotask(autosize);
    onSent();
    await messages.send(channel.id, content, reply, atts);
  }

  /** Last paste turned into a code block; Ctrl+Z right after restores the plain text. */
  let lastFence: { fenced: string; raw: string } | null = null;

  function key(e: KeyboardEvent) {
    if (lastFence && e.ctrlKey && !e.shiftKey && e.key.toLowerCase() === "z" && ta) {
      const f = lastFence;
      lastFence = null;
      const at = text.lastIndexOf(f.fenced);
      if (at >= 0) {
        e.preventDefault();
        ta.setSelectionRange(at, at + f.fenced.length);
        insertText(f.raw);
        return;
      }
    }
    if (e.key.length === 1 || e.key === "Backspace" || e.key === "Delete") lastFence = null;
    if (candidates.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        mentionSel = (mentionSel + (e.key === "ArrowDown" ? 1 : -1) + candidates.length) % candidates.length;
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pickMention(candidates[mentionSel]!);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        mentionQuery = null;
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      void submit();
    } else if (e.key === "Escape" && (editing || replyTo)) {
      e.preventDefault();
      if (editing) text = drafts.get(channel.id) ?? "";
      editing = null;
      replyTo = null;
    } else if (e.key === "ArrowUp" && !text && !editing) {
      e.preventDefault();
      onEditLast();
    } else if (!e.ctrlKey && e.key.length === 1) {
      messages.typingNow(channel.id);
    }
  }

  function addFiles(list: FileList | File[], secretsChecked = false) {
    if (!rights.attach) {
      toast(terr("attach_denied"), "info");
      return;
    }
    const all = [...list];
    const risky = secretsChecked ? [] : all.filter((f) => isSecretFileName(f.name));
    if (risky.length) {
      confirmSecrets(
        [],
        risky.map((f) => f.name),
        () => addFiles(all, true),
        () =>
          addFiles(
            all.filter((f) => !risky.includes(f)),
            true,
          ),
      );
      return;
    }
    for (const file of all.slice(0, 10 - files.length)) {
      const p: Pending = $state({
        key: keySeq++,
        file,
        progress: 0,
        handle: null as unknown as UploadHandle,
      });
      p.handle = upload(channel.id, file, (f) => (p.progress = f));
      files.push(p);
      p.handle.promise.then(
        (att) => (p.att = att),
        (err) => {
          const code = errorCode(err);
          if (code === "aborted") files = files.filter((x) => x.key !== p.key);
          else p.error = code;
        },
      );
    }
  }

  function removeFile(p: Pending) {
    if (!p.att && !p.error) p.handle.abort();
    files = files.filter((x) => x.key !== p.key);
  }

  /** Inserts at the caret through the editing stack, so Ctrl+Z keeps working. */
  function insertText(s: string) {
    if (!ta) return;
    ta.focus();
    if (!document.execCommand("insertText", false, s)) {
      const a = ta.selectionStart;
      text = text.slice(0, a) + s + text.slice(ta.selectionEnd);
    }
  }

  /** Inside a ``` block the user is writing code by hand: no auto fence. */
  function inFence(pos: number): boolean {
    return (text.slice(0, pos).match(/```/g)?.length ?? 0) % 2 === 1;
  }

  function paste(e: ClipboardEvent) {
    const fl = e.clipboardData?.files;
    if (fl && fl.length) {
      e.preventDefault();
      addFiles(fl);
      return;
    }
    // Too long for a message → attach as snippet file (not while editing:
    // edits cannot add attachments, the length check reports it on save).
    const pasted = e.clipboardData?.getData("text/plain") ?? "";
    if (!pasted || editing || !ta) return;
    const kept = text.slice(0, ta.selectionStart) + text.slice(ta.selectionEnd);
    if (charCount(kept) + charCount(pasted) <= app.limits.messageChars) {
      // Commands/code become a code block with a copy button for the reader.
      const fence = inFence(ta.selectionStart) ? null : codeFence(pasted);
      if (!fence) return;
      const before = text.slice(0, ta.selectionStart);
      const after = text.slice(ta.selectionEnd);
      const block =
        (before && !before.endsWith("\n") ? "\n" : "") +
        fence.text +
        (after && !after.startsWith("\n") ? "\n" : "");
      if (charCount(kept) + charCount(block) > app.limits.messageChars) return;
      e.preventDefault();
      insertText(block);
      lastFence = { fenced: block, raw: pasted };
      toast(t("chat.pastedAsCode"));
      return;
    }
    if (!rights.attach) return; // no snippet file: the length check reports it
    e.preventDefault();
    if (new Blob([pasted]).size > MAX_SNIPPET_BYTES) {
      toast(t("chat.snippetTooLarge"), "error");
      return;
    }
    confirmSecrets(findSecrets(pasted), [], () => {
      addFiles([snippetFile(pasted)], true);
      toast(t("chat.snippetAttached"));
    });
  }

  let picker = $state<HTMLInputElement>();
</script>

<div
  class="composer"
  class:dragging
  role="region"
  aria-label={t("chat.composer")}
  ondragover={(e) => {
    if (e.dataTransfer?.types.includes("Files")) {
      e.preventDefault();
      dragging = true;
    }
  }}
  ondragleave={() => (dragging = false)}
  ondrop={(e) => {
    e.preventDefault();
    dragging = false;
    if (e.dataTransfer?.files.length) addFiles(e.dataTransfer.files);
  }}
>
  {#if candidates.length}
    <div class="mentions" role="listbox" aria-label={t("chat.mentionSuggestions")}>
      {#each candidates as u, i (u.id)}
        <div
          class="cand"
          class:sel={i === mentionSel}
          role="option"
          tabindex="-1"
          aria-selected={i === mentionSel}
          onmousedown={(e) => {
            e.preventDefault();
            pickMention(u);
          }}
        >
          <Avatar name={u.displayName} id={u.id} size={20} />
          <span>{u.displayName}</span>
          <span class="un">@{u.username}</span>
        </div>
      {/each}
    </div>
  {/if}
  {#if replyTo || editing}
    <div class="banner">
      <span>
        {#if editing}{t("chat.editing")}{:else}{t("chat.replyingTo", {
            name: app.user(replyTo!.authorId)?.displayName ?? t("common.unknownUser"),
          })}{/if}
      </span>
      <button
        type="button"
        aria-label={t("common.cancel")}
        onclick={() => {
          if (editing) text = drafts.get(channel.id) ?? "";
          editing = null;
          replyTo = null;
        }}><X size={14} strokeWidth={2} /></button
      >
    </div>
  {/if}
  {#if files.length}
    <div class="files">
      {#each files as f (f.key)}
        <div class="chip" class:err={f.error}>
          <FileIcon size={16} strokeWidth={1.5} />
          <span class="fn">{f.file.name}</span>
          <span class="meta"
            >{f.error
              ? terr(f.error)
              : f.att
                ? formatBytes(f.file.size)
                : `${Math.round(f.progress * 100)} %`}</span
          >
          {#if !f.att && !f.error}<span class="bar" style:width="{f.progress * 100}%"></span>{/if}
          <button type="button" aria-label={t("common.delete")} onclick={() => removeFile(f)}
            ><X size={12} strokeWidth={2} /></button
          >
        </div>
      {/each}
    </div>
  {/if}
  {#if rights.slow && !editing}
    <p class="slow" aria-live="polite">
      {wait ? t("rules.composerSlow", { n: wait }) : t("rules.slowActive", { time: slowText(rights.slow) })}
    </p>
  {/if}
  {#if !rights.send && !editing}
    <p class="readonly">
      <Megaphone size={15} strokeWidth={1.75} />
      {authors.length
        ? t("rules.composerAnnounce", { roles: authors.join(", ") })
        : t("rules.composerReadOnly")}
    </p>
  {:else}
    <div class="box">
      {#if rights.attach}
        <button
          type="button"
          class="ib"
          aria-label={t("chat.attach")}
          onclick={() => picker?.click()}
          disabled={!!editing}><Plus size={18} strokeWidth={1.75} /></button
        >
        <input
          bind:this={picker}
          type="file"
          multiple
          hidden
          onchange={(e) => {
            const fl = (e.currentTarget as HTMLInputElement).files;
            if (fl) addFiles(fl);
            (e.currentTarget as HTMLInputElement).value = "";
          }}
        />
      {/if}
      <textarea
        bind:this={ta}
        bind:value={text}
        rows="1"
        placeholder={t("chat.placeholder", { name: app.channelTitle(channel) })}
        aria-label={t("chat.composer")}
        maxlength={8000}
        oninput={() => {
          autosize();
          updateMention();
        }}
        onkeydown={key}
        onclick={updateMention}
        onpaste={paste}></textarea>
      <div class="emo">
        <button type="button" class="ib" aria-label={t("chat.emoji")} onclick={() => (emoji = !emoji)}
          ><SmilePlus size={18} strokeWidth={1.75} /></button
        >
        {#if emoji}
          <div class="emo-pop">
            <EmojiPicker
              onpick={(e) => {
                text += e;
                ta?.focus();
              }}
              onclose={() => (emoji = false)}
            />
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<Sheet bind:open={secretOpen} title={t("secrets.title")} width={440} onclose={() => secretDecide(false)}>
  <p class="secret-body">{t("secrets.body")}</p>
  <ul class="secret-list">
    {#each secretAsk?.hits ?? [] as h (h.kind + h.hint)}
      <li><span>{t(`secrets.kind.${h.kind}`)}</span> <code>{h.hint}</code></li>
    {/each}
    {#each secretAsk?.files ?? [] as f (f)}
      <li><span>{t("secrets.file")}</span> <code>{f}</code></li>
    {/each}
  </ul>
  {#snippet footer()}
    <Button autofocus onclick={() => secretDecide(false)}>{t("secrets.edit")}</Button>
    <Button variant="danger" onclick={() => secretDecide(true)}>{t("secrets.sendAnyway")}</Button>
  {/snippet}
</Sheet>

<style>
  .composer {
    position: relative;
    padding: 0 16px 16px;
    flex: none;
  }
  .dragging .box {
    outline: 2px dashed var(--accent);
  }
  .slow {
    margin: 0 0 4px 4px;
    font-size: var(--fs-small);
    color: var(--text-2);
  }
  .readonly {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    padding: 11px 12px;
    border-radius: var(--r-lg);
    background: var(--bg-input);
    border: 1px solid var(--separator);
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .box {
    display: flex;
    align-items: flex-end;
    gap: 4px;
    padding: 6px;
    border-radius: var(--r-lg);
    background: var(--bg-input);
    border: 1px solid var(--separator);
  }
  textarea {
    flex: 1;
    resize: none;
    border: none;
    background: transparent;
    padding: 5px 4px;
    font-size: var(--fs-chat);
    line-height: 1.4;
    max-height: 40vh;
    min-height: 28px;
  }
  textarea::placeholder {
    color: var(--text-3);
  }
  .ib {
    width: 30px;
    height: 30px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }
  .ib:hover:not(:disabled) {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .emo {
    position: relative;
  }
  .emo-pop {
    position: absolute;
    right: 0;
    bottom: 38px;
    z-index: 30;
  }
  .banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 10px;
    margin-bottom: -1px;
    border-radius: var(--r-lg) var(--r-lg) 0 0;
    background: var(--bg-active);
    font-size: var(--fs-callout);
    color: var(--text-2);
  }
  .banner button {
    border: none;
    background: none;
    color: var(--text-2);
    display: inline-flex;
  }
  .files {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 6px;
  }
  .chip {
    position: relative;
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: 260px;
    height: 32px;
    padding: 0 6px 0 10px;
    border-radius: var(--r-md);
    background: var(--bg-elevated);
    border: 1px solid var(--separator);
    overflow: hidden;
    font-size: var(--fs-callout);
  }
  .chip.err {
    border-color: var(--danger);
  }
  .fn {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    color: var(--text-2);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .err .meta {
    color: var(--danger);
  }
  .bar {
    position: absolute;
    left: 0;
    bottom: 0;
    height: 2px;
    background: var(--accent);
  }
  .chip button {
    border: none;
    background: none;
    color: var(--text-2);
    display: inline-flex;
  }
  .mentions {
    position: absolute;
    left: 16px;
    right: 16px;
    bottom: 100%;
    margin-bottom: 4px;
    padding: 4px;
    border-radius: var(--r-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    z-index: 30;
  }
  .cand {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 30px;
    padding: 0 8px;
    border-radius: var(--r-sm);
  }
  .cand.sel {
    background: var(--accent);
    color: var(--on-accent);
  }
  .un {
    opacity: 0.6;
    font-size: var(--fs-callout);
  }
  .secret-body {
    margin: 0 0 10px;
    color: var(--text-2);
  }
  .secret-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .secret-list li {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
  }
  .secret-list code {
    font-family: var(--font-mono);
    font-size: 0.9em;
    color: var(--text-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
