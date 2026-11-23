const list = document.querySelector('#devices');
const columns = document.querySelector('#columns');
const filter = document.querySelector('#filter');
const count = document.querySelector('#device-count');
const status = document.querySelector('#status');
const statusLabel = document.querySelector('#status-label');
const empty = document.querySelector('#empty');
const emptyTitle = document.querySelector('#empty-title');
const emptyDescription = document.querySelector('#empty-description');
const provider = document.querySelector('#provider');

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' });
const relativeFormat = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'short' });
const rows = new Map();
let snapshot = { state: 'connecting', devices: [] };

const statuses = {
  connecting: ['Connecting', 'Connecting to Bluetooth discovery', 'Device observations will appear here as soon as the scanner is ready.'],
  reconnecting: ['Reconnecting', 'Connection interrupted', 'The dashboard is reconnecting to the Kubernetes API.'],
  unavailable: ['Scanner unavailable', 'Bluetooth discovery unavailable', 'Check that Bluetooth is on and the scanner has permission to use it.'],
  live: ['Live', 'No devices seen yet', 'Nearby devices will appear as soon as they advertise.'],
};

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function createRow() {
  const item = element('li');
  const disclosure = element('details');
  const summary = element('summary', 'device-grid');
  const identity = element('span');
  const name = element('span', 'device-name');
  const id = element('code', 'device-id');
  identity.append(name, id);
  const signal = element('span', 'signal');
  const meter = element('span', 'signal-meter');
  meter.setAttribute('aria-hidden', 'true');
  const bars = [4, 7, 10, 13].map((height) => {
    const bar = element('i');
    bar.style.setProperty('--bar-height', `${height}px`);
    return bar;
  });
  meter.append(...bars);
  const rssi = element('span');
  signal.append(meter, rssi);
  const seen = element('time', 'seen');
  const chevron = element('span', 'chevron');
  chevron.setAttribute('aria-hidden', 'true');
  summary.append(identity, signal, seen, chevron);
  const fields = element('dl', 'details');
  disclosure.append(summary, fields);
  item.append(disclosure);
  return { item, name, id, bars, rssi, seen, fields };
}

function formatDate(iso) {
  const date = new Date(iso);
  return Number.isFinite(date.valueOf()) ? dateFormat.format(date) : 'Unknown';
}

function refreshSeen(seen) {
  const seconds = Math.max(0, Math.floor((Date.now() - Date.parse(seen.dateTime)) / 1000));
  if (!Number.isFinite(seconds)) {
    seen.textContent = 'Unknown';
    return;
  }
  const [value, unit] = seconds < 60
    ? [seconds, 'second']
    : seconds < 3600 ? [Math.floor(seconds / 60), 'minute'] : [Math.floor(seconds / 3600), 'hour'];
  seen.textContent = relativeFormat.format(-value, unit);
}

function updateRow(row, device) {
  const details = device.status ?? {};
  row.name.textContent = details.localName || 'Unnamed device';
  row.id.textContent = device.metadata.name;

  const rssi = details.rssiDbm;
  const strength = typeof rssi !== 'number' ? 0 : rssi >= -55 ? 4 : rssi >= -67 ? 3 : rssi >= -80 ? 2 : 1;
  row.rssi.textContent = typeof rssi === 'number' ? `${rssi} dBm` : 'Unknown';
  row.bars.forEach((bar, index) => bar.toggleAttribute('data-active', index < strength));
  row.seen.dateTime = details.lastSeen ?? '';
  row.seen.title = formatDate(details.lastSeen);
  refreshSeen(row.seen);

  const values = [
    ['First seen', formatDate(details.firstSeen)],
    ['Last seen', formatDate(details.lastSeen)],
    ['Transmit power', typeof details.txPowerDbm === 'number' ? `${details.txPowerDbm} dBm` : 'Unknown'],
    ['Connectable', typeof details.connectable === 'boolean' ? (details.connectable ? 'Yes' : 'No') : 'Unknown'],
  ];
  if (details.advertisedServiceUUIDs?.length) values.push(['Advertised services', details.advertisedServiceUUIDs.join(', ')]);
  if (details.manufacturerCompanyIDs?.length) {
    values.push(['Manufacturer IDs', details.manufacturerCompanyIDs.map((id) => `0x${id.toString(16).padStart(4, '0').toUpperCase()}`).join(', ')]);
  }
  row.fields.replaceChildren(...values.map(([label, value]) => {
    const entry = element('div');
    entry.append(element('dt', null, label), element('dd', null, value));
    return entry;
  }));
}

function render() {
  const [label, title, description] = statuses[snapshot.state] ?? statuses.reconnecting;
  const live = snapshot.state === 'live';
  const query = filter.value.trim().toLowerCase();
  const visible = live ? snapshot.devices.filter((device) => {
    return `${device.status?.localName || 'Unnamed device'} ${device.metadata.name}`.toLowerCase().includes(query);
  }) : [];
  visible.sort((a, b) =>
    (a.status?.localName ?? '').localeCompare(b.status?.localName ?? '') ||
    a.metadata.name.localeCompare(b.metadata.name),
  );

  status.dataset.state = snapshot.state;
  if (statusLabel.textContent !== label) statusLabel.textContent = label;
  provider.textContent = snapshot.provider === 'replay' ? 'Replay demo · Read-only inventory' : 'Read-only inventory';
  count.hidden = !live;
  count.textContent = query ? `${visible.length} of ${snapshot.devices.length}` : String(snapshot.devices.length);
  filter.disabled = !live;
  columns.hidden = visible.length === 0;
  empty.hidden = visible.length > 0;
  const filtered = live && query && snapshot.devices.length > 0;
  const nextTitle = filtered ? 'No matching devices' : title;
  const nextDescription = filtered ? 'Try another name or device ID.' : description;
  if (emptyTitle.textContent !== nextTitle) emptyTitle.textContent = nextTitle;
  if (emptyDescription.textContent !== nextDescription) emptyDescription.textContent = nextDescription;

  const keys = new Set(visible.map((device) => device.metadata.uid));
  for (const [uid, row] of rows) {
    if (!keys.has(uid)) {
      row.item.remove();
      rows.delete(uid);
    }
  }
  visible.forEach((device, index) => {
    const uid = device.metadata.uid;
    const row = rows.get(uid) ?? createRow();
    rows.set(uid, row);
    updateRow(row, device);
    if (list.children[index] !== row.item) list.insertBefore(row.item, list.children[index] ?? null);
  });
}

filter.addEventListener('input', render);
setInterval(() => {
  for (const row of rows.values()) refreshSeen(row.seen);
}, 1000);

const events = new EventSource('/events');
events.onmessage = ({ data }) => {
  snapshot = JSON.parse(data);
  render();
};
events.onerror = () => {
  snapshot = { ...snapshot, state: 'reconnecting', devices: [] };
  render();
};
