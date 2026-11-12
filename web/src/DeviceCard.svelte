<script lang="ts">
  import type { MatterNode } from './api';
  import { clusterName, endpoints, formatValue, hex, nodeMaker, nodeName } from './nodes';

  let { node }: { node: MatterNode } = $props();

  let open = $state(false);

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
        <li>
          <span class="type">{part.type || `Endpoint ${part.endpoint}`}</span>
          <span class="values">
            {#if part.on !== undefined}
              <span class="pill" class:on={part.on}>{part.on ? 'On' : 'Off'}</span>
            {/if}
            {#if part.level !== undefined}<span class="value">{part.level}%</span>{/if}
            {#if part.kelvin !== undefined}<span class="value">{part.kelvin} K</span>{/if}
          </span>
        </li>
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

  .endpoints li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.5rem 0.7rem;
    border-radius: 8px;
    background: var(--bg);
  }

  .offline .endpoints li {
    opacity: 0.6;
  }

  .type {
    font-size: 0.9rem;
  }

  .values {
    flex: none;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    font-variant-numeric: tabular-nums;
  }

  .value {
    font-size: 0.9rem;
    color: var(--muted);
    white-space: nowrap;
  }

  .pill {
    min-width: 2.6rem;
    padding: 0.1rem 0.55rem;
    border-radius: 999px;
    border: 1px solid var(--line);
    font-size: 0.8rem;
    font-weight: 600;
    text-align: center;
    color: var(--muted);
  }

  .pill.on {
    border-color: var(--accent);
    background: var(--accent);
    color: var(--accent-text);
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
