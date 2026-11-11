<script lang="ts">
  // Settings → Shortcuts: global hotkeys (work while a game has focus) and
  // the fixed in-app shortcuts.
  import { prefs, savePrefs } from "../../lib/prefs.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { voice } from "../../lib/voice/voice.svelte";
  import Kbd from "../../lib/ui/Kbd.svelte";
  import KeyRecorder from "./KeyRecorder.svelte";
  import { isWeb } from "../../lib/native";

  const a = prefs.audio;
  function changed() {
    savePrefs();
    void voice.restartInput();
  }
</script>

<p class="gh">{isWeb ? t("web.keysTitle") : t("keys.global")}</p>
<div class="group">
  <div class="row">
    <span class="two">{t("keys.ptt")}<small>{t("keys.pttHint")}</small></span>
    <KeyRecorder label={t("keys.ptt")} bind:keys={a.pttKeys} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("tray.mute")}</span>
    <KeyRecorder label={t("tray.mute")} bind:keys={a.muteKeys} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("tray.deafen")}</span>
    <KeyRecorder label={t("tray.deafen")} bind:keys={a.deafenKeys} onchange={changed} />
  </div>
</div>
<p class="note">{isWeb ? t("web.keysNote") : t("keys.globalNote")}</p>

<p class="gh">{t("keys.inApp")}</p>
<div class="group">
  <div class="row"><span>{t("nav.quickSwitch")}</span><Kbd keys={[t("kbd.ctrl"), "K"]} /></div>
  <div class="row"><span>{t("search.title")}</span><Kbd keys={[t("kbd.ctrl"), "F"]} /></div>
</div>
