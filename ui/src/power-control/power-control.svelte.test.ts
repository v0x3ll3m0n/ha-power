import { describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import setup from '../index';
import {
  type Answer,
  type Call,
  fakePrinter,
  refusal,
  type Stub,
  stubHost,
} from '../test/host.svelte';

// The whole module, driven the way the app drives it: the stub host runs the
// bundle's own setup, the setup registers the two components, and each case
// renders the one it wants. Nothing imports past the entry, so what these
// cases prove is what a person would see.

const PRINTER = 'p3f2a1c9b';
const ROUTE = `/api/v1/modules/com.github.aux.v0x3ll3m0n.ha-power/power/${PRINTER}`;

const state = (on: boolean, available = true): Answer => ({
  status: 200,
  body: { on, available, entity: 'switch.mk4_plug' },
});

const started = (
  answers: readonly (Answer | ((call: Call) => Answer))[],
  printerState: Record<string, unknown> = { '/state': 'IDLE' },
): Stub => {
  const stub = stubHost(answers, [fakePrinter(PRINTER, printerState)]);
  void setup(stub.host);

  return stub;
};

type Slot = 'device-card' | 'device-section';

const drawn = async (stub: Stub, slot: Slot = 'device-section') => {
  const entry = stub.registry.sections(slot)[0];
  expect(entry, `the module registered no ${slot}`).toBeDefined();

  await render(entry!.component, { props: { deviceId: PRINTER } });
};

const toggle = () => page.getByRole('switch');

// The module ships no stylesheet and the tests render unstyled, so the switch
// has no size to click on. The click is the DOM's own, which is the event the
// browser would deliver.
const flip = (): void => {
  (toggle().element() as HTMLButtonElement).click();
};

describe('the power control', () => {
  it('registers the two slots its manifest declares, and nothing else', () => {
    const stub = started([state(true)]);

    expect(stub.registry.sections('device-card')).toHaveLength(1);
    expect(stub.registry.sections('device-section')).toHaveLength(1);
  });

  it('shows a loading state, then the state Home Assistant answered', async () => {
    const stub = started([state(true)]);
    await drawn(stub);

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
    await expect.element(page.getByText('On', { exact: true })).toBeVisible();

    // One read, at the module's own address, with the browser's session and no
    // token in the page.
    expect(stub.calls).toHaveLength(1);
    const read = stub.calls[0]!;
    expect(read.url).toBe(ROUTE);
    expect(read.method).toBe('GET');
    expect(read.authorization).toBeNull();
    expect(read.credentials).toBe('same-origin');
  });

  it('shows Off when the plug is off, on the card line as well', async () => {
    const stub = started([state(false)]);
    await drawn(stub, 'device-card');

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'false');
    await expect.element(page.getByText('Off', { exact: true })).toBeVisible();
  });

  it('turns the plug on with one POST and shows what the module answered', async () => {
    const stub = started([state(false), { status: 200, body: { on: true } }]);
    await drawn(stub);

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'false');
    flip();

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(stub.calls).toHaveLength(2);

    const write = stub.calls[1]!;
    expect(write.url).toBe(ROUTE);
    expect(write.method).toBe('POST');
    expect(write.body).toBe('{"on":true}');
    // The browser never names an entity and never holds a token.
    expect(write.body).not.toContain('entity');
    expect(write.authorization).toBeNull();
  });

  it('turns the plug off when the printer is idle and cool', async () => {
    const stub = started([state(true), { status: 200, body: { on: false } }], {
      '/state': 'FINISHED',
      '/tools/0/nozzle_temp': 30,
    });
    await drawn(stub);

    await expect.element(toggle()).toBeEnabled();
    flip();

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(stub.calls[1]!.body).toBe('{"on":false}');
  });

  it.each([
    ['PRINTING', /while the printer is printing/],
    ['PAUSED', /while the print is paused/],
    ['BUSY', /while the printer is busy/],
  ])(
    'disables power off while the printer is %s, and says why',
    async (value, why) => {
      const stub = started([state(true)], { '/state': value });
      await drawn(stub);

      await expect.element(toggle()).toBeDisabled();
      await expect.element(page.getByText(why)).toBeVisible();
    },
  );

  it('disables power off while the nozzle is above 50 degrees, and says why', async () => {
    const stub = started([state(true)], {
      '/state': 'FINISHED',
      '/tools/0/nozzle_temp': 51,
    });
    await drawn(stub);

    await expect.element(toggle()).toBeDisabled();
    await expect.element(page.getByText(/nozzle is above 50/)).toBeVisible();
  });

  it('enables power off again when the printer cools', async () => {
    const stub = started([state(true)], {
      '/state': 'FINISHED',
      '/tools/0/nozzle_temp': 180,
    });
    await drawn(stub);

    await expect.element(toggle()).toBeDisabled();

    stub.fleet.devices = [
      fakePrinter(PRINTER, {
        '/state': 'FINISHED',
        '/tools/0/nozzle_temp': 49,
      }),
    ];

    await expect.element(toggle()).toBeEnabled();
  });

  it('never locks power on, whatever the printer last reported', async () => {
    const stub = started([state(false)], {
      '/state': 'PRINTING',
      '/tools/0/nozzle_temp': 200,
    });
    await drawn(stub);

    await expect.element(toggle()).toBeEnabled();
  });

  it('says a printer with no plug has none, and draws no toggle', async () => {
    const stub = started([refusal(404, 'ha-power/NOT_MAPPED')]);
    await drawn(stub);

    await expect
      .element(page.getByText('No plug is set for this printer'))
      .toBeVisible();
    expect(page.getByRole('switch').elements()).toHaveLength(0);
  });

  it('shows an unavailable plug and keeps the toggle disabled', async () => {
    const stub = started([state(false, false)]);
    await drawn(stub);

    await expect.element(toggle()).toBeDisabled();
    await expect
      .element(page.getByText('The plug is unavailable in Home Assistant'))
      .toBeVisible();
  });

  it('shows Home Assistant being down on load, and loads again on request', async () => {
    const stub = started([
      refusal(502, 'ha-power/HA_UNREACHABLE'),
      state(true),
    ]);
    await drawn(stub);

    await expect
      .element(page.getByText('Home Assistant is not reachable'))
      .toBeVisible();

    await page.getByRole('button', { name: 'Try again' }).click();

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(stub.calls).toHaveLength(2);
  });

  it('keeps the toggle where the plug is when the module refuses, and says the reason', async () => {
    const stub = started([state(true), refusal(409, 'ha-power/PRINTER_BUSY')]);
    await drawn(stub);
    await expect.element(toggle()).toBeEnabled();

    flip();

    await expect.element(page.getByText('The printer is in use')).toBeVisible();
    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
    await expect.element(toggle()).toBeEnabled();
  });

  it('tells a person who may not operate the printer that they may not switch it', async () => {
    const stub = started([state(true), refusal(403, 'ha-power/NOT_ALLOWED')]);
    await drawn(stub);
    await expect.element(toggle()).toBeEnabled();

    flip();

    await expect
      .element(page.getByText('You may not switch this printer'))
      .toBeVisible();
    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
  });

  it('says a failure with no code in the words of the catalogue', async () => {
    const stub = started([state(false), { status: 500, body: 'broken' }]);
    await drawn(stub);
    await expect.element(toggle()).toBeEnabled();

    flip();

    await expect
      .element(page.getByText('The power could not be changed'))
      .toBeVisible();
  });

  it('sends one request for a double click while the first is in flight', async () => {
    const stub = started([state(false), { status: 200, body: { on: true } }]);
    await drawn(stub);
    await expect.element(toggle()).toBeEnabled();

    // Two clicks in one tick: the second finds the control busy.
    flip();
    flip();

    await expect.element(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(stub.calls.filter((call) => call.method === 'POST')).toHaveLength(1);
  });
});
