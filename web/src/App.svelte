<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { ApiError, getCommissioning, getHealth, getHub, getSession, openEvents, signIn, signOut } from './api';
  import type { Commissioning, Hub, MatterNode } from './api';
  import AddDevice from './AddDevice.svelte';
  import DeviceCard from './DeviceCard.svelte';
  import { nodeName, withAttributes, withNode, withStatus } from './nodes';

  type View = 'loading' | 'unreachable' | 'signin' | 'home';

  let view = $state<View>('loading');
  let version = $state('');
  let hub = $state<Hub | null>(null);
  let token = $state('');
  let busy = $state(false);
  let message = $state('');
  let nodes = $state<MatterNode[]>([]);
  // null until the stream has delivered the nodes once.
  let live = $state<boolean | null>(null);
  let commissioning = $state<Commissioning | null>(null);
  // What the hub said after this page removed a device.
  let notice = $state('');

  function removed(name: string, fromDevice: boolean) {
    notice = fromDevice
      ? `${name} was removed. To add it again, put it into pairing mode, or reset it to factory defaults.`
      : `${name} was removed from the hub only. It still holds the hub's fabric: reset it to factory defaults before adding it again.`;
  }
  let closeEvents: (() => void) | null = null;

  function stopEvents() {
    closeEvents?.();
    closeEvents = null;
    live = null;
  }

  function startEvents() {
    stopEvents();
    closeEvents = openEvents({
      nodes: (all) => {
        nodes = all;
        live = true;
        // A commissioning may have run or ended while the stream was away.
        getCommissioning()
          .then((res) => {
            if (res.commissioning && (!commissioning || res.commissioning.id >= commissioning.id)) {
              commissioning = res.commissioning;
            }
          })
          .catch(() => {
            // Told again by the next event.
          });
      },
      status: (event) => (nodes = withStatus(nodes, event)),
      attributes: (event) => (nodes = withAttributes(nodes, event)),
      node: (node) => (nodes = withNode(nodes, node)),
      removed: (id) => (nodes = nodes.filter((n) => n.id !== id)),
      commissioning: (c) => (commissioning = c),
      connection: (open) => {
        if (open) {
          return;
        }
        if (live !== null) {
          live = false;
        }
        // The stream also breaks when the session has ended; the browser
        // would then try again for ever.
        getSession()
          .then((session) => {
            if (session.authRequired && !session.authenticated) {
              stopEvents();
              hub = null;
              view = 'signin';
            }
          })
          .catch(() => {
            // The hub is unreachable; the stream keeps trying.
          });
      },
    });
  }

  async function load() {
    message = '';
    try {
      const [health, session] = await Promise.all([getHealth(), getSession()]);
      version = health.version;
      if (session.authRequired && !session.authenticated) {
        stopEvents();
        hub = null;
        view = 'signin';
        return;
      }
      hub = await getHub();
      view = 'home';
      startEvents();
    } catch (err) {
      stopEvents();
      if (err instanceof ApiError && err.status === 401) {
        hub = null;
        view = 'signin';
        return;
      }
      view = 'unreachable';
      message = err instanceof Error ? err.message : String(err);
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || token.trim() === '') {
      return;
    }
    busy = true;
    message = '';
    try {
      await signIn(token.trim());
      token = '';
      await load();
    } catch (err) {
      message = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  async function leave() {
    stopEvents();
    busy = true;
    try {
      await signOut();
    } catch {
      // The session is checked again below either way.
    } finally {
      busy = false;
    }
    await load();
  }

  onMount(load);
  onDestroy(stopEvents);
</script>

<header>
  <div class="brand">
    <span class="mark" aria-hidden="true"></span>
    <h1>go-matter-hub</h1>
  </div>
  <div class="meta">
    {#if version}<span class="version">v{version}</span>{/if}
    {#if view === 'home' && hub?.authRequired}
      <button class="quiet" onclick={leave} disabled={busy}>Sign out</button>
    {/if}
  </div>
</header>

<main>
  {#if view === 'loading'}
    <p class="muted" role="status">Connecting to the hub…</p>
  {:else if view === 'unreachable'}
    <section class="card">
      <h2>The hub is not responding</h2>
      <p class="muted">{message}</p>
      <button onclick={load}>Try again</button>
    </section>
  {:else if view === 'signin'}
    <section class="card narrow">
      <h2>Sign in</h2>
      <p class="muted">
        Enter the access token of this hub. It is in the file <code>token</code> in the hub's data
        directory.
      </p>
      <form onsubmit={submit}>
        <label for="token">Access token</label>
        <input
          id="token"
          type="password"
          autocomplete="current-password"
          spellcheck="false"
          bind:value={token}
          disabled={busy}
        />
        {#if message}<p class="error" role="alert">{message}</p>{/if}
        <button type="submit" disabled={busy || token.trim() === ''}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </section>
  {:else}
    <div class="status" role="status">
      {#if live === false}
        <span class="dot lost" aria-hidden="true"></span> Connection to the hub lost. Trying again…
      {:else}
        <span class="dot" aria-hidden="true"></span> Hub is running
        {#if hub?.fabricId}<span class="fabric">· Fabric {hub.fabricId}</span>{/if}
      {/if}
    </div>
    <AddDevice
      {commissioning}
      nameOf={(id) => {
        const node = nodes.find((n) => n.id === id);
        return node ? nodeName(node) : `Node ${id}`;
      }}
    />
    {#if notice}
      <section class="card notice" role="status">
        <p>{notice}</p>
        <button class="dismiss" onclick={() => (notice = '')}>Dismiss</button>
      </section>
    {/if}
    {#if live === null}
      <p class="muted">Loading devices…</p>
    {:else if nodes.length === 0}
      <section class="card">
        <p class="muted last">No devices yet. Add one with its setup code.</p>
      </section>
    {:else}
      <div class="devices">
        {#each nodes as node (node.id)}
          <DeviceCard {node} onremoved={removed} />
        {/each}
      </div>
    {/if}
  {/if}
</main>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.9rem 1.25rem;
    border-bottom: 1px solid var(--line);
    background: var(--surface);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }

  .mark {
    width: 0.9rem;
    height: 0.9rem;
    border-radius: 50%;
    border: 3px solid var(--accent);
  }

  h1 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 650;
    letter-spacing: -0.01em;
  }

  .meta {
    display: flex;
    align-items: center;
    gap: 0.9rem;
  }

  .version {
    color: var(--muted);
    font-size: 0.85rem;
    font-variant-numeric: tabular-nums;
  }

  main {
    max-width: 46rem;
    margin: 0 auto;
    padding: 2rem 1.25rem 4rem;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius);
    padding: 1.5rem;
  }

  .narrow {
    max-width: 26rem;
    margin: 0 auto;
  }

  h2 {
    margin: 0 0 0.5rem;
    font-size: 1.25rem;
    letter-spacing: -0.01em;
  }

  .muted {
    color: var(--muted);
    margin: 0 0 1rem;
    overflow-wrap: anywhere;
  }

  .status {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 1.25rem;
    font-size: 0.9rem;
    color: var(--muted);
  }

  .dot {
    flex: none;
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: var(--ok);
  }

  .dot.lost {
    background: var(--danger);
  }

  .fabric {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  .devices {
    display: grid;
    /* minmax(0, …) lets a card be narrower than a wide table inside it. */
    grid-template-columns: minmax(0, 1fr);
    gap: 0.9rem;
  }

  .notice {
    margin-bottom: 0.9rem;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }

  .notice p {
    margin: 0;
  }

  .dismiss {
    flex: none;
    padding: 0.3rem 0.6rem;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: transparent;
    color: var(--text);
    cursor: pointer;
  }

  .muted.last {
    margin: 0;
  }

  form {
    display: grid;
    gap: 0.6rem;
  }

  label {
    font-size: 0.9rem;
    font-weight: 600;
  }

  input {
    width: 100%;
    padding: 0.6rem 0.7rem;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--bg);
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

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.9em;
  }
</style>
