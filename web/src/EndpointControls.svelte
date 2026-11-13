<script lang="ts">
  import { invoke } from './api';
  import type { Command } from './api';
  import { kelvinToMireds } from './nodes';
  import type { EndpointSummary } from './nodes';

  let { node, part, online }: { node: string; part: EndpointSummary; online: boolean } = $props();

  const ON_OFF = 0x0006;
  const LEVEL_CONTROL = 0x0008;
  const COLOR_CONTROL = 0x0300;
  // Half a second, in the tenths the commands count in.
  const TRANSITION = 5;
  // Leave the Options attribute of the light in charge.
  const OPTIONS = { OptionsMask: 0, OptionsOverride: 0 };

  const swatches = [
    { name: 'Red', hue: 0 },
    { name: 'Orange', hue: 30 },
    { name: 'Yellow', hue: 55 },
    { name: 'Green', hue: 120 },
    { name: 'Cyan', hue: 180 },
    { name: 'Blue', hue: 230 },
    { name: 'Purple', hue: 275 },
    { name: 'Pink', hue: 320 },
  ];

  let busy = $state(false);
  let problem = $state('');
  // How long a slider keeps the position it was let go at, waiting for the
  // light to report it.
  const SETTLE_MS = 3000;

  // A slider shows the reported state, except while a person moves it and
  // until the light reports the value it was let go at; otherwise it would
  // jump back to the old value in between.
  class Slider {
    value = $state(0);
    #held = false;
    #target: number | null = null;
    #timer: ReturnType<typeof setTimeout> | undefined;

    hold() {
      this.#held = true;
    }

    /** Lets go at the current value, and waits for the light to report it. */
    release(): number {
      this.#held = false;
      this.#target = this.value;
      clearTimeout(this.#timer);
      this.#timer = setTimeout(() => {
        this.#target = null;
        this.value = this.#reported ?? this.value;
      }, SETTLE_MS);
      return this.value;
    }

    #reported: number | undefined;

    /** Follows a reported value. */
    report(reported: number | undefined) {
      this.#reported = reported;
      if (reported === undefined || this.#held) {
        return;
      }
      if (this.#target !== null) {
        if (reported !== this.#target) {
          return;
        }
        this.#target = null;
        clearTimeout(this.#timer);
      }
      this.value = reported;
    }

    stop() {
      clearTimeout(this.#timer);
    }
  }

  const level = new Slider();
  const kelvin = new Slider();

  $effect(() => level.report(part.level === undefined ? undefined : Math.max(1, part.level)));
  $effect(() => kelvin.report(part.kelvin));
  $effect(() => () => {
    level.stop();
    kelvin.stop();
  });

  async function send(command: Omit<Command, 'endpoint'>) {
    busy = true;
    problem = '';
    try {
      await invoke(node, { endpoint: part.endpoint, ...command });
    } catch (err) {
      problem = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  function setOn(on: boolean) {
    send({ cluster: ON_OFF, command: on ? 0x01 : 0x00 });
  }

  function commitLevel() {
    // CurrentLevel runs from 1 to 254. MoveToLevelWithOnOff also turns the
    // light on.
    const value = Math.min(254, Math.max(1, Math.round((level.release() / 100) * 254)));
    send({ cluster: LEVEL_CONTROL, command: 0x04, fields: { Level: value, TransitionTime: TRANSITION, ...OPTIONS } });
  }

  function commitKelvin() {
    let mireds = kelvinToMireds(kelvin.release());
    // The slider's ends are rounded to 50 K; keep the light's own range.
    if (part.miredsRange) {
      mireds = Math.min(part.miredsRange.max, Math.max(part.miredsRange.min, mireds));
    }
    send({
      cluster: COLOR_CONTROL,
      command: 0x0a,
      fields: { ColorTemperatureMireds: mireds, TransitionTime: TRANSITION, ...OPTIONS },
    });
  }

  function setHue(hue: number) {
    send({
      cluster: COLOR_CONTROL,
      command: 0x06,
      fields: { Hue: Math.round((hue / 360) * 254), Saturation: 254, TransitionTime: TRANSITION, ...OPTIONS },
    });
  }

  const disabled = $derived(!online || busy);
  const currentColor = $derived(
    part.colorMode === 'hs' && part.hue !== undefined && part.saturation !== undefined
      ? `hsl(${part.hue} ${Math.round(part.saturation * 100)}% 55%)`
      : '',
  );
  const label = $derived(part.type || `Endpoint ${part.endpoint}`);
</script>

<li class="endpoint">
  <div class="row">
    <span class="type">
      {#if currentColor}<span class="current" style:background={currentColor} aria-hidden="true"></span>{/if}
      {label}
    </span>
    {#if part.on !== undefined}
      <button
        class="switch"
        class:on={part.on}
        role="switch"
        aria-checked={part.on}
        aria-label="{label} power"
        {disabled}
        onclick={() => setOn(!part.on)}
      >
        <span class="knob" aria-hidden="true"></span>
        <span class="state">{part.on ? 'On' : 'Off'}</span>
      </button>
    {/if}
  </div>

  {#if part.level !== undefined}
    <label class="slider">
      <span class="name">Brightness</span>
      <input
        type="range"
        min="1"
        max="100"
        bind:value={level.value}
        {disabled}
        oninput={() => level.hold()}
        onchange={commitLevel}
      />
      <span class="reading">{level.value}%</span>
    </label>
  {/if}

  {#if part.kelvinRange && part.kelvin !== undefined}
    <label class="slider">
      <span class="name">White</span>
      <input
        class="warmth"
        type="range"
        min={part.kelvinRange.min}
        max={part.kelvinRange.max}
        step="50"
        bind:value={kelvin.value}
        {disabled}
        oninput={() => kelvin.hold()}
        onchange={commitKelvin}
      />
      <span class="reading">{kelvin.value} K</span>
    </label>
  {:else if part.kelvin !== undefined}
    <p class="reading-only">{part.kelvin} K</p>
  {/if}

  {#if part.hueSaturation}
    <div class="swatches" role="group" aria-label="{label} color">
      {#each swatches as swatch (swatch.hue)}
        <button
          class="swatch"
          style:background="hsl({swatch.hue} 90% 55%)"
          aria-label={swatch.name}
          title={swatch.name}
          {disabled}
          onclick={() => setHue(swatch.hue)}
        ></button>
      {/each}
    </div>
  {/if}

  {#if problem}<p class="problem" role="alert">{problem}</p>{/if}
</li>

<style>
  .endpoint {
    display: grid;
    gap: 0.6rem;
    padding: 0.6rem 0.75rem;
    border-radius: 8px;
    background: var(--bg);
  }

  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }

  .type {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9rem;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .current {
    flex: none;
    width: 0.8rem;
    height: 0.8rem;
    border-radius: 50%;
    border: 1px solid var(--line);
  }

  .switch {
    flex: none;
    display: flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.2rem 0.65rem 0.2rem 0.25rem;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--surface);
    color: var(--muted);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .knob {
    width: 1.1rem;
    height: 1.1rem;
    border-radius: 50%;
    background: var(--line);
    transition: background 0.15s;
  }

  .switch.on {
    /* The knob moves to the right when the light is on. */
    flex-direction: row-reverse;
    padding: 0.2rem 0.25rem 0.2rem 0.65rem;
    border-color: var(--accent);
    background: var(--accent);
    color: var(--accent-text);
  }

  .switch.on .knob {
    background: var(--accent-text);
  }

  .switch:disabled,
  .swatch:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .state {
    min-width: 1.6rem;
    text-align: center;
  }

  .slider {
    display: grid;
    grid-template-columns: 5.5rem minmax(0, 1fr) 4.2rem;
    align-items: center;
    gap: 0.7rem;
    font-size: 0.85rem;
  }

  .name {
    color: var(--muted);
  }

  input[type='range'] {
    width: 100%;
    accent-color: var(--accent);
  }

  input.warmth {
    accent-color: #d9a441;
  }

  .reading {
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }

  .reading-only {
    margin: 0;
    font-size: 0.85rem;
    color: var(--muted);
  }

  .swatches {
    display: flex;
    flex-wrap: wrap;
    gap: 0.45rem;
  }

  .swatch {
    width: 1.7rem;
    height: 1.7rem;
    padding: 0;
    border: 2px solid var(--surface);
    border-radius: 50%;
    box-shadow: 0 0 0 1px var(--line);
    cursor: pointer;
  }

  .swatch:not(:disabled):hover {
    box-shadow: 0 0 0 2px var(--accent);
  }

  .problem {
    margin: 0;
    color: var(--danger);
    font-size: 0.82rem;
    overflow-wrap: anywhere;
  }
</style>
