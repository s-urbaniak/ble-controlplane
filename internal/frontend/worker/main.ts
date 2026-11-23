import { CustomObjectsApi, KubeConfig, Watch } from '@kubernetes/client-node';
import { setTimeout as delay } from 'node:timers/promises';

const resource = {
  group: 'generic.example.com',
  version: 'v1alpha1',
  namespace: 'local',
  plural: 'bledevices',
};
const path = `/apis/${resource.group}/${resource.version}/namespaces/${resource.namespace}/${resource.plural}`;

interface BLEDevice {
  metadata: { name: string; namespace: string; resourceVersion: string; uid: string };
  status: {
    localName?: string;
    rssiDbm?: number;
    txPowerDbm?: number;
    connectable?: boolean;
    advertisedServiceUUIDs?: string[];
    manufacturerCompanyIDs?: number[];
    firstSeen: string;
    lastSeen: string;
  };
}

interface BLEDeviceList {
  metadata: { resourceVersion: string };
  items: BLEDevice[];
}

type State = 'connecting' | 'live' | 'unavailable' | 'reconnecting';
type WatchObject = BLEDevice & { code?: number; message?: string };

const config = new KubeConfig();
config.loadFromDefault();
const api = config.makeApiClient(CustomObjectsApi);
const watch = new Watch(config);
const devices = new Map<string, BLEDevice>();

let state: State = 'connecting';
let pending: string | undefined;
let waitingForDrain = false;
let publishTimer: NodeJS.Timeout | undefined;

function flush() {
  if (pending === undefined || waitingForDrain) return;
  waitingForDrain = !process.stdout.write(pending);
  pending = undefined;
  if (waitingForDrain) {
    process.stdout.once('drain', () => {
      waitingForDrain = false;
      flush();
    });
  }
}

function publish(next: State) {
  clearTimeout(publishTimer);
  publishTimer = undefined;
  state = next;
  pending = JSON.stringify({
    state,
    provider: process.env.BLE_PROVIDER ?? 'native',
    devices: state === 'live' ? [...devices.values()] : [],
  }) + '\n';
  flush();
}

function publishChange() {
  // A scan can update many devices together. Emit the latest snapshot once.
  publishTimer ??= setTimeout(() => publish('live'), 100);
}

function watchFrom(resourceVersion: string): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    let controller: AbortController | undefined;
    let finished = false;
    const finish = (error?: unknown) => {
      if (finished) return;
      finished = true;
      controller?.abort();
      if (error) reject(error);
      else resolve(resourceVersion);
    };

    watch.watch(path, { resourceVersion, allowWatchBookmarks: true, timeoutSeconds: 25 }, (type, object: WatchObject) => {
      if (finished) return;
      if (type === 'ERROR') {
        finish(Object.assign(new Error(object.message ?? 'Kubernetes watch failed'), { code: object.code }));
        return;
      }
      if (!object.metadata?.resourceVersion) return;
      resourceVersion = object.metadata.resourceVersion;
      if (type === 'BOOKMARK' || object.metadata.namespace !== resource.namespace) return;
      if (type === 'DELETED') {
        devices.delete(object.metadata.uid);
      } else if (type === 'ADDED' || type === 'MODIFIED') {
        devices.set(object.metadata.uid, object);
      }
      publishChange();
    }, finish).then((request) => {
      controller = request;
      if (finished) controller.abort();
    }, finish);
  });
}

function errorStatus(error: unknown) {
  if (error === null || typeof error !== 'object') return undefined;
  if ('statusCode' in error && typeof error.statusCode === 'number') return error.statusCode;
  if ('code' in error && typeof error.code === 'number') return error.code;
}

async function run() {
  publish('connecting');
  let version = '';
  let retry = 500;
  for (;;) {
    try {
      if (!version) {
        const list: BLEDeviceList = await api.listNamespacedCustomObject(resource);
        if (!list.metadata?.resourceVersion || !Array.isArray(list.items)) {
          throw new Error('The BLE list did not contain a snapshot and resource version');
        }
        devices.clear();
        for (const item of list.items) {
          if (item.metadata.namespace === resource.namespace) devices.set(item.metadata.uid, item);
        }
        version = list.metadata.resourceVersion;
        publish('live');
      }

      version = await watchFrom(version);
      retry = 500;
      // A normal watch timeout resumes at the most recent event or bookmark.
      await delay(100);
    } catch (error) {
      // An expired or interrupted watch needs a fresh list before showing data.
      version = '';
      const code = errorStatus(error);
      const next = code === 503 ? 'unavailable' : 'reconnecting';
      if (state !== next) {
        if (code !== 410 && code !== 503) console.error('BLE API connection interrupted', error);
        publish(next);
      }
      await delay(retry);
      retry = Math.min(retry * 2, 3000);
    }
  }
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
