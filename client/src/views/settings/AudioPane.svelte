<script lang="ts">
  // Settings → Voice & audio: devices, volumes, mic test with level meter and
  // threshold, input mode, push-to-talk, processing and sounds.
  import { onDestroy, onMount } from "svelte";
  import { prefs, savePrefs } from "../../lib/prefs.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { listAudioDevices, MicChain, type MicLevel } from "../../lib/voice/mic";
  import { voice } from "../../lib/voice/voice.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import SegmentedControl from "../../lib/ui/SegmentedControl.svelte";
  import Select from "../../lib/ui/Select.svelte";
  import Slider from "../../lib/ui/Slider.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";
  import { toast } from "../../lib/ui/toast.svelte";
  import KeyRecorder from "./KeyRecorder.svelte";
  import { isWeb } from "../../lib/native";

  const a = prefs.audio;
  let inputs = $state<{ value: string; label: string }[]>([]);
  let outputs = $state<{ value: string; label: string }[]>([]);
  let test = $state<MicChain | null>(null);
  let testing = $state(false);
  let meter = $state<MicLevel>({ level: -100, threshold: a.vadThreshold, open: false });

  async function loadDevices() {
    try {
      const d = await listAudioDevices();
      const def = { value: "", label: t("audio.systemDefault") };
      inputs = [
        def,
        ...d.inputs.map((x, i) => ({ value: x.deviceId, label: x.label || `${t("audio.mic")} ${i + 1}` })),
      ];
      outputs = [
        def,
        ...d.outputs.map((x, i) => ({
          value: x.deviceId,
          label: x.label || `${t("audio.speaker")} ${i + 1}`,
        })),
      ];
      // A remembered device that is gone falls back to the default (hotplug).
      if (a.inputDevice && !d.inputs.some((x) => x.deviceId === a.inputDevice))
        inputs.push({ value: a.inputDevice, label: t("audio.missing") });
      if (a.outputDevice && !d.outputs.some((x) => x.deviceId === a.outputDevice))
        outputs.push({ value: a.outputDevice, label: t("audio.missing") });
    } catch {
      inputs = [{ value: "", label: t("audio.systemDefault") }];
      outputs = inputs;
    }
  }

  onMount(() => {
    void loadDevices();
    navigator.mediaDevices?.addEventListener("devicechange", loadDevices);
  });

  onDestroy(() => {
    navigator.mediaDevices?.removeEventListener("devicechange", loadDevices);
    void stopTest();
  });

  function opts() {
    return {
      deviceId: a.inputDevice,
      echoCancellation: a.echoCancellation,
      autoGainControl: a.autoGainControl,
      noise: a.noise,
      inputVolume: a.inputVolume,
      mode: a.mode === "ptt" ? ("open" as const) : a.mode,
      vadAuto: a.vadAuto,
      vadThreshold: a.vadThreshold,
    };
  }

  async function startTest() {
    if (testing) return;
    testing = true;
    try {
      const m = await MicChain.create(opts());
      m.onLevel = (l) => (meter = l);
      m.setMeter(true);
      m.setMonitor(true, a.outputDevice);
      test = m;
      void loadDevices(); // labels are available now
    } catch (e) {
      testing = false;
      const n = (e as DOMException).name;
      toast(
        n === "NotAllowedError"
          ? t("voice.micDenied")
          : n === "NotFoundError"
            ? t("voice.noMic")
            : t("voice.micFailed"),
        "error",
      );
    }
  }

  async function stopTest() {
    testing = false;
    const m = test;
    test = null;
    meter = { level: -100, threshold: a.vadThreshold, open: false };
    await m?.close();
  }

  function changed() {
    savePrefs();
    void test?.update(opts());
    test?.setMonitor(true, a.outputDevice);
    void voice.applyAudioPrefs();
  }

  // Meter scale: -80 dBFS .. 0 dBFS → 0..100 %.
  const pos = (db: number) => Math.max(0, Math.min(100, ((db + 80) / 80) * 100));
  const thr = $derived(a.vadAuto ? meter.threshold : a.vadThreshold);
</script>

<p class="gh">{t("audio.devices")}</p>
<div class="group">
  <div class="row">
    <span>{t("audio.input")}</span>
    <Select label={t("audio.input")} bind:value={a.inputDevice} options={inputs} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("audio.inputVolume")}</span>
    <Slider
      label={t("audio.inputVolume")}
      bind:value={a.inputVolume}
      min={0}
      max={200}
      step={5}
      reset={100}
      format={(v) => `${v} %`}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span>{t("audio.output")}</span>
    <Select label={t("audio.output")} bind:value={a.outputDevice} options={outputs} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("audio.outputVolume")}</span>
    <Slider
      label={t("audio.outputVolume")}
      bind:value={a.outputVolume}
      min={0}
      max={200}
      step={5}
      reset={100}
      format={(v) => `${v} %`}
      onchange={changed}
    />
  </div>
</div>

<div class="group">
  <div class="row">
    <span class="two">
      {t("audio.micTest")}
      <small>{t("audio.micTestHint")}</small>
    </span>
    {#if testing}
      <Button onclick={() => void stopTest()}>{t("audio.stopTest")}</Button>
    {:else}
      <Button variant="primary" onclick={() => void startTest()}>{t("audio.startTest")}</Button>
    {/if}
  </div>
  <div class="row meter-row">
    <div
      class="meter"
      role="meter"
      aria-label={t("audio.level")}
      aria-valuemin={-80}
      aria-valuemax={0}
      aria-valuenow={Math.round(meter.level)}
    >
      <div class="fill" class:open={meter.open} style:width="{pos(meter.level)}%"></div>
      {#if a.mode === "vad"}<div class="thr" style:left="{pos(thr)}%" title={t("audio.threshold")}></div>{/if}
    </div>
  </div>
</div>

<div class="group">
  <div class="row">
    <span>{t("audio.mode")}</span>
    <SegmentedControl
      label={t("audio.mode")}
      bind:value={a.mode}
      onchange={changed}
      options={[
        { value: "vad" as const, label: t("audio.vad") },
        { value: "ptt" as const, label: t("audio.ptt") },
      ]}
    />
  </div>
  {#if a.mode === "vad"}
    <div class="row">
      <span class="two">
        {t("audio.autoThreshold")}
        <small
          >{t("audio.autoThresholdHint")}{#if a.vadAuto && testing}&nbsp;· {Math.round(meter.threshold)} dB{/if}</small
        >
      </span>
      <Toggle label={t("audio.autoThreshold")} bind:checked={a.vadAuto} onchange={changed} />
    </div>
    {#if !a.vadAuto}
      <div class="row">
        <span>{t("audio.threshold")}</span>
        <Slider
          label={t("audio.threshold")}
          bind:value={a.vadThreshold}
          min={-80}
          max={0}
          reset={-50}
          format={(v) => `${v} dB`}
          onchange={changed}
        />
      </div>
    {/if}
  {:else}
    <div class="row">
      <span class="two">
        {t("audio.pttKey")}
        <small>{t("audio.pttKeyHint")}</small>
      </span>
      <KeyRecorder label={t("audio.pttKey")} bind:keys={a.pttKeys} onchange={changed} />
    </div>
    <div class="row">
      <span>{t("audio.pttDelay")}</span>
      <Slider
        label={t("audio.pttDelay")}
        bind:value={a.pttDelay}
        min={0}
        max={1000}
        step={10}
        reset={200}
        format={(v) => `${v} ms`}
        onchange={changed}
      />
    </div>
  {/if}
</div>
{#if a.mode === "ptt"}<p class="note">{isWeb ? t("web.keysNote") : t("audio.pttElevatedNote")}</p>{/if}

<p class="gh">{t("audio.processing")}</p>
<div class="group">
  <div class="row">
    <span class="two">
      {t("audio.noise")}
      <small>{t("audio.noiseHint")}</small>
    </span>
    <SegmentedControl
      label={t("audio.noise")}
      bind:value={a.noise}
      onchange={changed}
      options={[
        { value: "off" as const, label: t("audio.noiseOff") },
        { value: "standard" as const, label: t("audio.noiseStandard") },
        { value: "strong" as const, label: t("audio.noiseStrong") },
      ]}
    />
  </div>
  <div class="row">
    <span>{t("audio.echo")}</span>
    <Toggle label={t("audio.echo")} bind:checked={a.echoCancellation} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("audio.agc")}</span>
    <Toggle label={t("audio.agc")} bind:checked={a.autoGainControl} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("audio.sounds")}</span>
    <Toggle label={t("audio.sounds")} bind:checked={a.sounds} onchange={changed} />
  </div>
</div>

<style>
  .meter-row {
    padding-top: 12px;
    padding-bottom: 14px;
  }
  .meter {
    position: relative;
    flex: 1;
    height: 8px;
    border-radius: var(--r-full);
    background: var(--bg-active);
    overflow: hidden;
  }
  .fill {
    position: absolute;
    inset: 0 auto 0 0;
    background: var(--warning);
    border-radius: var(--r-full);
    transition: width 50ms linear;
  }
  .fill.open {
    background: var(--success);
  }
  .thr {
    position: absolute;
    top: -2px;
    bottom: -2px;
    width: 2px;
    margin-left: -1px;
    background: var(--text-1);
  }
</style>
