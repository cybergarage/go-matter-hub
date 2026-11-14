<script lang="ts">
  import { onDestroy } from 'svelte';
  import { ApiError, startCommissioning } from './api';
  import type { Commissioning, CommissioningErrorCode } from './api';

  interface Props {
    /** The latest commissioning the hub told. */
    commissioning: Commissioning | null;
    /** The name of a node, for the message after it joined. */
    nameOf: (id: string) => string;
  }

  let { commissioning, nameOf }: Props = $props();

  type Network = 'bluetooth' | 'ip';

  const SSID_KEY = 'go-matter-hub.ssid';

  function storedSSID(): string {
    try {
      return localStorage.getItem(SSID_KEY) ?? '';
    } catch {
      return '';
    }
  }

  function storeSSID(ssid: string) {
    try {
      localStorage.setItem(SSID_KEY, ssid);
    } catch {
      // The SSID is only a convenience; it is typed again next time.
    }
  }

  let open = $state(false);
  let code = $state('');
  let network = $state<Network>('bluetooth');
  let ssid = $state(storedSSID());
  let password = $state('');
  let sending = $state(false);
  let message = $state('');
  // The commissioning this page started, followed until it ends.
  let startedId = $state<number | null>(null);
  let now = $state(Date.now());
  let clock: ReturnType<typeof setInterval> | null = null;

  // A commissioning which runs is shown whoever started it, so that a page
  // which was reloaded meanwhile follows it; one which ended only when this
  // page started it.
  const shown = $derived(
    commissioning && (commissioning.state === 'running' || commissioning.id === startedId) ? commissioning : null,
  );
  const running = $derived(shown?.state === 'running');

  $effect(() => {
    if (running && clock === null) {
      now = Date.now();
      clock = setInterval(() => (now = Date.now()), 1000);
    } else if (!running && clock !== null) {
      clearInterval(clock);
      clock = null;
    }
  });

  // The code and the password stay in the form for another try, and are
  // dropped once the device was added.
  $effect(() => {
    if (shown?.state === 'succeeded' && shown.id === startedId) {
      code = '';
      password = '';
    }
  });

  onDestroy(() => {
    if (clock !== null) {
      clearInterval(clock);
    }
  });

  const elapsed = $derived.by(() => {
    if (!shown) {
      return '';
    }
    const seconds = Math.max(0, Math.floor((now - Date.parse(shown.startedAt)) / 1000));
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
  });

  const hints: Record<CommissioningErrorCode, string> = {
    not_found:
      'No device with this setup code was found. Check the code, keep the device close to the hub, and make sure it is powered on and waiting to be set up.',
    wifi_required:
      'The device was found over Bluetooth only. Choose “New device” and give the Wi-Fi network it is to join.',
    not_in_commissioning_mode:
      'The device is on the network but not waiting to be set up. If another app or hub has it, open a pairing window there. Otherwise — after it was removed from this hub, or an earlier attempt failed midway — reset it to factory defaults.',
    timeout: 'The device did not finish in time. Keep it close to the hub and try again.',
    failed: 'The device could not be added. Try again; if it fails again, reset the device.',
  };

  const valid = $derived(
    code.trim() !== '' && (network === 'ip' || (ssid.trim() !== '' && password.length >= 8)),
  );

  function toggle() {
    open = !open;
    message = '';
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (sending || running || !valid) {
      return;
    }
    sending = true;
    message = '';
    const wifi = network === 'bluetooth' ? { ssid: ssid.trim(), password } : undefined;
    try {
      const res = await startCommissioning(code.trim(), wifi);
      startedId = res.commissioning.id;
      if (wifi) {
        storeSSID(wifi.ssid);
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === 'busy') {
        message = 'Another device is being added. Wait for it to finish.';
      } else {
        message = err instanceof Error ? err.message : String(err);
      }
    } finally {
      sending = false;
    }
  }

  function dismiss() {
    startedId = null;
    open = false;
  }
</script>

<div class="head">
  <h2>Devices</h2>
  {#if !shown}
    <button class="quiet" onclick={toggle} aria-expanded={open}>{open ? 'Cancel' : 'Add device'}</button>
  {/if}
</div>

{#if shown}
  <section class="card progress" class:failed={shown.state === 'failed'} aria-live="polite">
    {#if shown.state === 'running'}
      <div class="row">
        <span class="spinner" aria-hidden="true"></span>
        <strong>Adding a device…</strong>
        <span class="elapsed">{elapsed}</span>
      </div>
      <p class="muted">
        {shown.overBluetooth
          ? 'Looking for it over Bluetooth, then handing it the Wi-Fi network.'
          : 'Looking for it on the network.'}
        This can take up to three minutes; you can leave this page meanwhile.
      </p>
    {:else if shown.state === 'succeeded'}
      <div class="row"><strong>Device added</strong></div>
      <p class="muted">{shown.node ? `${nameOf(shown.node)} joined the hub.` : 'The device joined the hub.'}</p>
      <button class="quiet" onclick={dismiss}>Done</button>
    {:else}
      <div class="row"><strong>The device was not added</strong></div>
      <p class="hint">{hints[shown.errorCode ?? 'failed']}</p>
      {#if shown.error}<p class="detail">{shown.error}</p>{/if}
      <button class="quiet" onclick={() => ((startedId = null), (open = true))}>Try again</button>
    {/if}
  </section>
{:else if open}
  <section class="card">
    <form onsubmit={submit}>
      <label for="code">Setup code</label>
      <input
        id="code"
        autocomplete="off"
        autocapitalize="characters"
        spellcheck="false"
        placeholder="MT:… or 11-digit code"
        bind:value={code}
        disabled={sending}
      />
      <p class="note">The text of the device's QR code, or the numeric code printed beside it.</p>

      <fieldset disabled={sending}>
        <legend>The device is</legend>
        <label class="choice">
          <input type="radio" name="network" value="bluetooth" bind:group={network} />
          <span><strong>New</strong> — not on the network yet; found over Bluetooth</span>
        </label>
        <label class="choice">
          <input type="radio" name="network" value="ip" bind:group={network} />
          <span><strong>Already on the network</strong> — Wi-Fi or Ethernet, with a pairing window open</span>
        </label>
      </fieldset>

      {#if network === 'bluetooth'}
        <label for="ssid">Wi-Fi network (2.4 GHz)</label>
        <input id="ssid" autocomplete="off" spellcheck="false" bind:value={ssid} disabled={sending} />
        <label for="wifi-password">Wi-Fi password</label>
        <input
          id="wifi-password"
          type="password"
          autocomplete="off"
          spellcheck="false"
          bind:value={password}
          disabled={sending}
        />
        <p class="note">Handed to the device only. The hub does not store it.</p>
      {/if}

      {#if message}<p class="error" role="alert">{message}</p>{/if}
      <button type="submit" disabled={sending || !valid}>{sending ? 'Starting…' : 'Add device'}</button>
    </form>
  </section>
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.75rem;
  }

  h2 {
    margin: 0;
    font-size: 1.25rem;
    letter-spacing: -0.01em;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius);
    padding: 1.25rem 1.5rem;
    margin-bottom: 0.9rem;
  }

  .progress {
    border-color: var(--accent);
  }

  .progress.failed {
    border-color: var(--danger);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.4rem;
  }

  .elapsed {
    margin-left: auto;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .spinner {
    width: 1rem;
    height: 1rem;
    border-radius: 50%;
    border: 2px solid var(--line);
    border-top-color: var(--accent);
    animation: spin 0.9s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .spinner {
      animation: none;
      border-color: var(--accent);
    }
  }

  .muted,
  .hint {
    margin: 0 0 0.75rem;
    color: var(--muted);
  }

  .hint {
    color: var(--text);
  }

  .detail {
    margin: 0 0 0.75rem;
    color: var(--muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  form {
    display: grid;
    gap: 0.5rem;
  }

  label {
    font-size: 0.9rem;
    font-weight: 600;
  }

  input:not([type='radio']) {
    width: 100%;
    padding: 0.6rem 0.7rem;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--bg);
  }

  .note {
    margin: -0.2rem 0 0.4rem;
    font-size: 0.8rem;
    color: var(--muted);
  }

  fieldset {
    display: grid;
    gap: 0.4rem;
    margin: 0.3rem 0 0.5rem;
    padding: 0;
    border: 0;
  }

  legend {
    padding: 0;
    margin-bottom: 0.3rem;
    font-size: 0.9rem;
    font-weight: 600;
  }

  .choice {
    display: flex;
    align-items: flex-start;
    gap: 0.55rem;
    font-weight: 400;
  }

  .choice input {
    margin-top: 0.3rem;
    accent-color: var(--accent);
  }

  button {
    justify-self: start;
    padding: 0.55rem 1rem;
    border: 1px solid var(--accent);
    border-radius: 8px;
    background: var(--accent);
    color: var(--accent-text);
    font-weight: 600;
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.55;
    cursor: default;
  }

  button.quiet {
    padding: 0.35rem 0.7rem;
    border-color: var(--line);
    background: transparent;
    color: var(--text);
    font-weight: 500;
  }

  .error {
    margin: 0;
    color: var(--danger);
    font-size: 0.9rem;
  }
</style>
