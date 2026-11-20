import { describe, expect, it } from 'vitest';

import { lockOf } from './safety';
import { fakePrinter } from './test/host.svelte';

describe('the power off lock', () => {
  it.each([
    ['PRINTING', 'printing'],
    ['PAUSED', 'paused'],
    ['BUSY', 'busy'],
  ])('locks %s', (value, lock) => {
    expect(lockOf(fakePrinter('p', { '/state': value }))).toBe(lock);
  });

  it('locks a nozzle above 50 degrees and not one at 50', () => {
    expect(lockOf(fakePrinter('p', { '/tools/0/nozzle_temp': 50.5 }))).toBe(
      'hot',
    );
    expect(
      lockOf(fakePrinter('p', { '/tools/0/nozzle_temp': 50 })),
    ).toBeUndefined();
  });

  it('does not lock what it cannot read', () => {
    expect(lockOf(undefined)).toBeUndefined();
    expect(lockOf(fakePrinter('p'))).toBeUndefined();
    expect(
      lockOf(fakePrinter('p', { '/state': 7, '/tools/0/nozzle_temp': 'hot' })),
    ).toBeUndefined();
    expect(lockOf(fakePrinter('p', { '/state': 'IDLE' }))).toBeUndefined();
  });
});
