<script lang="ts">
  import { ApiError, removeNode } from './api';
  import type { MatterNode } from './api';
  import EndpointControls from './EndpointControls.svelte';
  import { clusterName, endpoints, formatValue, hex, nodeMaker, nodeName } from './nodes';

  interface Props {
    node: MatterNode;
    /** Called once the hub removed the node; the card then goes. */
    onremoved?: (name: string, fromDevice: boolean) => void;
  }

  let { node, onremoved }: Props = $props();

  let open = $state(false);

  // Removal: asked to confirm, then sent. When the device cannot leave the
  // fabric, the hub can still forget it, which is offered as a second step.
  type Removing = 'idle' | 'confirm' | 'sending' | 'local';
  let removing = $state<Removing>('idle');
  let removeError = $state('');

  async function remove(local: boolean) {
    const removedName = name;
    removing = 'sending';
    removeError = '';
    try {
      const res = await removeNode(node.id, local);
      removing = 'idle';
      onremoved?.(removedName, res.fromDevice);
    } catch (err) {
      if (err instanceof ApiError && (err.code === 'node_offline' || err.code === 'remove_failed')) {
        removeError =
          err.code === 'node_offline'
            ? 'The device is offline, so it cannot leave the fabric.'
            : `The device did not leave the fabric: ${err.message}`;
        removing = 'local';
      } else {
        removeError = err instanceof Error ? err.message : String(err);
        removing = 'confirm';
      }
    }
  }

  const name = $derived(nodeName(node));
  const maker = $derived(nodeMaker(node));
  const parts = $derived(endpoints(node));
  const statusLabel = $derived(
    node.status === 'online' ? 'Online' : node.status === 'offline' ? 'Offline' : 'Connecting…',
  );
</script>

<article class="device" class:offline={node.status !== 'online'}>
  <div class="head">
    <div class="title">
      <h3>{name}</h3>
      {#if maker}<p class="maker">{maker}</p>{/if}
    </div>
    <span class="badge {node.status}">{statusLabel}</span>
  </div>

  {#if node.status === 'offline' && node.error}
    <p class="problem">{node.error}</p>
  {/if}

  {#if parts.length > 0}
    <ul class="endpoints">
      {#each parts as part (part.endpoint)}
        <EndpointControls node={node.id} {part} online={node.status === 'online'} />
      {/each}
    </ul>
  {:else if node.status === 'connecting'}
    <p class="hint">Waiting for the first report.</p>
  {/if}

  <button class="toggle" aria-expanded={open} onclick={() => (open = !open)}>
    {open ? 'Hide' : 'Show'} attributes ({node.attributes.length})
  </button>

  {#if open}
    <div class="scroll">
      <table>
        <thead>
          <tr><th>Endpoint</th><th>Cluster</th><th>Attribute</th><th>Value</th></tr>
        </thead>
        <tbody>
          {#each node.attributes as a (`${a.endpoint}/${a.cluster}/${a.attribute}`)}
            <tr>
              <td>{a.endpoint}</td>
              <td>{clusterName(a.cluster)}</td>
              <td class="mono">{hex(a.attribute)}</td>
              <td class="mono value-cell">{formatValue(a)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <p class="node-id">Node {node.id}</p>
  {/if}

  {#if open || removing !== 'idle'}
    <div class="remove" aria-live="polite">
      {#if removing === 'idle'}
        <button class="danger quiet" onclick={() => (removing = 'confirm')}>Remove device…</button>
      {:else if removing === 'confirm' || removing === 'sending'}
        <p>
          Remove <strong>{name}</strong>? It leaves the hub's fabric, and the hub stops controlling it.
        </p>
        {#if removeError}<p class="problem">{removeError}</p>{/if}
        <div class="actions">
          <button class="danger" disabled={removing === 'sending'} onclick={() => remove(false)}>
            {removing === 'sending' ? 'Removing…' : 'Remove'}
          </button>
          <button class="quiet" disabled={removing === 'sending'} onclick={() => ((removing = 'idle'), (removeError = ''))}>
            Cancel
          </button>
        </div>
      {:else}
        <p class="problem">{removeError}</p>
        <p>
          The hub can forget it anyway. The device then still holds the hub's fabric, until you reset it to
          factory defaults.
        </p>
        <div class="actions">
          <button class="danger" onclick={() => remove(true)}>Remove from the hub only</button>
          <button class="quiet" onclick={() => ((removing = 'idle'), (removeError = ''))}>Cancel</button>
        </div>
      {/if}
    </div>
  {/if}
</article>

<style>
  .device {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius);
    padding: 1.1rem 1.25rem;
  }

  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }

  h3 {
    margin: 0;
    font-size: 1.05rem;
    letter-spacing: -0.01em;
    overflow-wrap: anywhere;
  }

  .maker,
  .hint,
  .node-id {
    margin: 0.15rem 0 0;
    color: var(--muted);
    font-size: 0.85rem;
  }

  .badge {
    flex: none;
    padding: 0.15rem 0.6rem;
    border-radius: 999px;
    border: 1px solid var(--line);
    font-size: 0.8rem;
    color: var(--muted);
    white-space: nowrap;
  }

  .badge.online {
    border-color: transparent;
    background: color-mix(in srgb, var(--ok) 16%, transparent);
    color: var(--ok);
  }

  .badge.offline {
    border-color: transparent;
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger);
  }

  .problem {
    margin: 0.6rem 0 0;
    color: var(--danger);
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }

  .endpoints {
    list-style: none;
    margin: 0.9rem 0 0;
    padding: 0;
    display: grid;
    gap: 0.4rem;
  }

  .offline .endpoints {
    opacity: 0.6;
  }

  .remove {
    margin-top: 0.9rem;
    padding-top: 0.8rem;
    border-top: 1px solid var(--line);
  }

  .remove p {
    margin: 0 0 0.6rem;
    font-size: 0.9rem;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .remove button {
    padding: 0.4rem 0.8rem;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: transparent;
    color: var(--text);
    font-size: 0.85rem;
    cursor: pointer;
  }

  .remove button.danger {
    border-color: var(--danger);
    background: var(--danger);
    color: var(--bg);
    font-weight: 600;
  }

  .remove button.danger.quiet {
    background: transparent;
    color: var(--danger);
    font-weight: 500;
  }

  .remove button:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .toggle {
    margin-top: 0.9rem;
    padding: 0;
    border: 0;
    background: none;
    color: var(--muted);
    font-size: 0.85rem;
    cursor: pointer;
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .scroll {
    margin-top: 0.7rem;
    max-height: 22rem;
    overflow: auto;
    border: 1px solid var(--line);
    border-radius: 8px;
  }

  table {
    width: 100%;
    /* Narrower than this, the table scrolls sideways inside its frame. */
    min-width: 30rem;
    border-collapse: collapse;
    font-size: 0.82rem;
  }

  th,
  td {
    padding: 0.35rem 0.6rem;
    text-align: left;
    vertical-align: top;
    border-bottom: 1px solid var(--line);
  }

  th {
    position: sticky;
    top: 0;
    background: var(--surface);
    color: var(--muted);
    font-weight: 600;
  }

  tr:last-child td {
    border-bottom: 0;
  }

  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }

  .value-cell {
    max-width: 18rem;
    overflow-wrap: anywhere;
  }
</style>
