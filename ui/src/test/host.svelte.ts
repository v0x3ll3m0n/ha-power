// A stub host, on the model of examples/spools/ui/src/test/host.ts and
// examples/nozzle-watch/ui/src/test/host.svelte.ts: everything the module
// receives, built over a fetch that answers from a script and a fleet a test
// writes.
//
// The registry is the library's own `SlotRegistry`, declared with the block
// `module.yaml` carries, so a registration outside it is refused here exactly
// as the app refuses it. The client is a real `PrusaConnect` in session mode,
// so the request the page causes is the request the browser would send: the
// path, the method and the absence of an Authorization header are readable.
import { PrusaConnect } from '@prusa/connect';
import type { Device, ReadonlyFleet } from '@prusa/connect-ui/domain';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type { ModuleContributes, ModuleHost } from '@prusa/connect-ui/slots';
import {
  buildRegister,
  noComponents,
  modulePageHref,
  SlotRegistry,
} from '@prusa/connect-ui/slots';
import { SvelteMap } from 'svelte/reactivity';

import english from '../locales/en.json';

export const NAMESPACE = 'com.github.aux.v0x3ll3m0n.ha-power';

/** The ceiling, written the way `../../module.yaml` declares it. */
export const CONTRIBUTES: ModuleContributes = {
  device_card: [{ id: 'power', title: 'Power' }],
  device_section: [{ id: 'power', title: 'Power' }],
};

/** One request the module made, as the stub recorded it. */
export interface Call {
  readonly url: string;
  readonly method: string;
  readonly credentials: RequestCredentials | undefined;
  readonly authorization: string | null;
  readonly body: string | undefined;
}

/** One scripted answer. A refusal carries the module's own problem document. */
export interface Answer {
  readonly status: number;
  readonly body: unknown;
  readonly contentType?: string;
}

/** A refusal as the Go half writes it. */
export const refusal = (status: number, code: string): Answer => ({
  status,
  contentType: 'application/problem+json',
  body: {
    type: 'about:blank',
    status,
    code,
    title: code,
    detail: code,
    params: {},
    retryable: false,
  },
});

/** One printer with every field the document declares, and a state tree a test fills. */
export const fakePrinter = (
  id: string,
  state: Record<string, unknown> = {},
): Device => ({
  actions: [],
  authorization_epoch: 1,
  capabilities: [],
  capability_epoch: 1,
  connected: true,
  driver: 'fake',
  firmware: '1.0.0',
  id,
  issues: [],
  kind: 'printer',
  labels: {},
  link: {
    counted: false,
    dropped_telemetry: 0,
    dropped_telemetry_per_minute: 0,
    measured_over_seconds: 60,
    refused_frames: 0,
    refused_frames_per_minute: 0,
  },
  model: 'MK4',
  name: id,
  profile_matched: true,
  serial: `serial-${id}`,
  state,
  unit_uid: `unit-${id}`,
  vendor: 'Prusa',
  verbs: null,
});

/** The fleet, with the surface a module is handed and a setter the test writes through. */
export class FakeFleet implements ReadonlyFleet {
  #devices = $state.raw<readonly Device[]>([]);
  readonly loading = false;
  readonly loaded = true;
  readonly error = undefined;

  constructor(devices: readonly Device[] = []) {
    this.#devices = devices;
  }

  get devices(): readonly Device[] {
    return this.#devices;
  }

  /** The one writer. A module holds the read-only half and cannot reach this. */
  set devices(rows: readonly Device[]) {
    this.#devices = rows;
  }

  device(id: string): Device | undefined {
    return this.#devices.find((row) => row.id === id);
  }
}

export interface Stub {
  host: ModuleHost;
  fleet: FakeFleet;
  registry: SlotRegistry;
  calls: Call[];
}

const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

const answered = ({ status, body, contentType }: Answer): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': contentType ?? 'application/json' },
  });

/**
 * A host whose fetch answers each call from `answers`, in order, and repeats
 * the last one after that. A reply may be a function of the call, for a test
 * that answers by method.
 */
export const stubHost = (
  answers: readonly (Answer | ((call: Call) => Answer))[],
  devices: readonly Device[] = [],
): Stub => {
  const registry = new SlotRegistry();
  registry.declare(NAMESPACE, CONTRIBUTES);
  registerCatalogue(NAMESPACE, 'en', english);

  const fleet = new FakeFleet(devices);
  const calls: Call[] = [];
  const records = new SvelteMap<string, string>();
  let made = 0;

  const fetcher: typeof globalThis.fetch = (input, init) => {
    const headers = new Headers(init?.headers);
    const call: Call = {
      url: addressOf(input),
      method: init?.method ?? 'GET',
      credentials: init?.credentials,
      authorization: headers.get('authorization'),
      body: typeof init?.body === 'string' ? init.body : undefined,
    };
    calls.push(call);

    const next = answers[Math.min(made, answers.length - 1)];
    made += 1;

    const reply =
      typeof next === 'function'
        ? next(call)
        : (next ?? { status: 200, body: {} });

    return Promise.resolve(answered(reply));
  };

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: { id: 'user:demo', grants: [] },
    fleet,
    t: translateFor(NAMESPACE),
    signal: new AbortController().signal,
    pageHref: (path) => modulePageHref(NAMESPACE, path),
    store: {
      get: (key) => records.get(key),
      set: (key, value) => {
        records.set(key, value);
      },
      delete: (key) => {
        records.delete(key);
      },
      keys: () => [...records.keys()].sort(),
    },
    components: noComponents,
    register: buildRegister({
      page: () => {
        throw new Error('ha-power adds no page');
      },
      section: (slot, id, component) => {
        registry.registerSection(NAMESPACE, slot, id, component);
      },
      action: () => {
        throw new Error('ha-power adds no action');
      },
      component: () => {
        throw new Error('ha-power exports no component');
      },
    }),
  };

  return { host, fleet, registry, calls };
};
