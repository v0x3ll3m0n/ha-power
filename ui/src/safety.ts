// When the page disables power off. The Go half checks the same rule before it
// calls Home Assistant, and that check is the one that holds. This one only
// keeps the button from offering a move the server will refuse, and says why.
//
// A device record carries `state`, the addressable state tree keyed by path.
// Its values are `unknown`, because the core publishes no schema for the tree,
// so each is narrowed here rather than trusted (typescript.md).
import type { Device } from '@prusa/connect-ui/domain';

/** Where the printer's state machine value is. */
export const STATE = '/state';

/** What the first head's nozzle reads, in degrees Celsius. */
export const NOZZLE = '/tools/0/nozzle_temp';

/** The nozzle temperature above which power off is refused. */
export const NOZZLE_LIMIT = 50;

/** Why power off is refused, as the key of the word that says it. */
export type Lock = 'printing' | 'paused' | 'busy' | 'hot';

const STATE_LOCKS: Readonly<Record<string, Lock>> = {
  PRINTING: 'printing',
  PAUSED: 'paused',
  BUSY: 'busy',
};

/**
 * Why a printer must not lose power now, or `undefined`. A printer that is not
 * in the fleet, or has reported nothing, is not locked: the server decides.
 */
export const lockOf = (device: Device | undefined): Lock | undefined => {
  const state = device?.state[STATE];
  if (typeof state === 'string' && state in STATE_LOCKS) {
    return STATE_LOCKS[state];
  }

  const nozzle = device?.state[NOZZLE];
  if (
    typeof nozzle === 'number' &&
    Number.isFinite(nozzle) &&
    nozzle > NOZZLE_LIMIT
  ) {
    return 'hot';
  }

  return undefined;
};
