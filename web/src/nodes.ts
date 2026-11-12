// What the UI derives from the attributes a node reports. The hub sends
// values as the node reported them; their meaning is applied here.

import type { Attribute, AttributesEvent, MatterNode, StatusEvent } from './api';

const BASIC_INFORMATION = 0x0028;
const DESCRIPTOR = 0x001d;
const ON_OFF = 0x0006;
const LEVEL_CONTROL = 0x0008;
const COLOR_CONTROL = 0x0300;

const clusterNames: Record<number, string> = {
  0x0003: 'Identify',
  0x0004: 'Groups',
  0x0006: 'On/Off',
  0x0008: 'Level Control',
  0x001d: 'Descriptor',
  0x001f: 'Access Control',
  0x0028: 'Basic Information',
  0x002a: 'OTA Software Update Requestor',
  0x0030: 'General Commissioning',
  0x0031: 'Network Commissioning',
  0x0033: 'General Diagnostics',
  0x0036: 'Wi-Fi Network Diagnostics',
  0x003c: 'Administrator Commissioning',
  0x003e: 'Operational Credentials',
  0x003f: 'Group Key Management',
  0x0062: 'Scenes Management',
  0x0090: 'Electrical Power Measurement',
  0x0091: 'Electrical Energy Measurement',
  0x0300: 'Color Control',
  0x0402: 'Temperature Measurement',
  0x0405: 'Relative Humidity Measurement',
  0x0406: 'Occupancy Sensing',
};

const deviceTypeNames: Record<number, string> = {
  0x000e: 'Aggregator',
  0x0013: 'Bridged Node',
  0x0016: 'Root Node',
  0x0100: 'On/Off Light',
  0x0101: 'Dimmable Light',
  0x010a: 'On/Off Plug-in Unit',
  0x010b: 'Dimmable Plug-in Unit',
  0x010c: 'Color Temperature Light',
  0x010d: 'Extended Color Light',
  0x0302: 'Temperature Sensor',
  0x0307: 'Humidity Sensor',
  0x0510: 'Electrical Sensor',
};

export function hex(value: number, digits = 4): string {
  return '0x' + value.toString(16).toUpperCase().padStart(digits, '0');
}

export function clusterName(cluster: number): string {
  return clusterNames[cluster] ?? `Cluster ${hex(cluster)}`;
}

function find(node: MatterNode, endpoint: number, cluster: number, attribute: number): Attribute | undefined {
  return node.attributes.find((a) => a.endpoint === endpoint && a.cluster === cluster && a.attribute === attribute);
}

function text(node: MatterNode, attribute: number): string {
  const value = find(node, 0, BASIC_INFORMATION, attribute)?.value;
  return typeof value === 'string' ? value.trim() : '';
}

/** The name a person gave the node, or else its product name. */
export function nodeName(node: MatterNode): string {
  return text(node, 5) || text(node, 3) || `Node ${node.id}`;
}

/** Vendor and product name, where the node reported them. */
export function nodeMaker(node: MatterNode): string {
  const vendor = text(node, 1);
  const product = text(node, 3);
  const label = text(node, 5);
  // The product name is the title when the node has no label.
  return [vendor, label ? product : ''].filter(Boolean).join(' · ');
}

export interface EndpointSummary {
  endpoint: number;
  type: string;
  on?: boolean;
  /** Brightness, 0 to 100. */
  level?: number;
  /** Color temperature in kelvin. */
  kelvin?: number;
}

function deviceType(node: MatterNode, endpoint: number): string {
  const list = find(node, endpoint, DESCRIPTOR, 0)?.value;
  if (!Array.isArray(list)) {
    return '';
  }
  // DeviceTypeList: structures with the device type in field 0.
  const names = list
    .map((entry) => (entry && typeof entry === 'object' ? (entry as Record<string, unknown>)['0'] : undefined))
    .filter((id): id is number => typeof id === 'number')
    .map((id) => deviceTypeNames[id] ?? `Device type ${hex(id)}`);
  return names.join(', ');
}

/** The application endpoints of a node: every endpoint but the root (0). */
export function endpoints(node: MatterNode): EndpointSummary[] {
  const ids = [...new Set(node.attributes.map((a) => a.endpoint))].filter((id) => id !== 0).sort((a, b) => a - b);
  return ids.map((endpoint) => {
    const summary: EndpointSummary = { endpoint, type: deviceType(node, endpoint) };
    const on = find(node, endpoint, ON_OFF, 0)?.value;
    if (typeof on === 'boolean') {
      summary.on = on;
    }
    // CurrentLevel runs from 1 to 254.
    const level = find(node, endpoint, LEVEL_CONTROL, 0)?.value;
    if (typeof level === 'number') {
      summary.level = Math.round((Math.min(level, 254) / 254) * 100);
    }
    // ColorTemperatureMireds: a million divided by the kelvin.
    const mireds = find(node, endpoint, COLOR_CONTROL, 7)?.value;
    if (typeof mireds === 'number' && mireds > 0) {
      summary.kelvin = Math.round(1_000_000 / mireds / 50) * 50;
    }
    return summary;
  });
}

export function formatValue(attribute: Attribute): string {
  if (attribute.status !== undefined) {
    return `status ${hex(attribute.status, 2)}`;
  }
  const value = attribute.value;
  if (typeof value === 'string') {
    return value;
  }
  return JSON.stringify(value);
}

const key = (a: Attribute) => `${a.endpoint}/${a.cluster}/${a.attribute}`;

function sorted(attributes: Attribute[]): Attribute[] {
  return [...attributes].sort(
    (a, b) => a.endpoint - b.endpoint || a.cluster - b.cluster || a.attribute - b.attribute,
  );
}

/** The nodes after a status event. */
export function withStatus(nodes: MatterNode[], event: StatusEvent): MatterNode[] {
  return nodes.map((n) => (n.id === event.node ? { ...n, status: event.status, error: event.error } : n));
}

/** The nodes after an attributes event. */
export function withAttributes(nodes: MatterNode[], event: AttributesEvent): MatterNode[] {
  return nodes.map((n) => {
    if (n.id !== event.node) {
      return n;
    }
    if (event.priming) {
      return { ...n, attributes: sorted(event.attributes) };
    }
    const merged = new Map(n.attributes.map((a) => [key(a), a]));
    for (const a of event.attributes) {
      merged.set(key(a), a);
    }
    return { ...n, attributes: sorted([...merged.values()]) };
  });
}
