// What one printer's power control knows. A class in a `.svelte.ts` file, as
// the house rule for state a component owns and a test must build without
// mocking modules (svelte-5.md): the service comes in through the constructor.
import { ApiError } from '@prusa/connect';

import type { PowerService } from './power';

/** What the control is doing. `error` is a load that failed. */
export type Phase = 'loading' | 'ready' | 'not-mapped' | 'error';

/** A refusal's code, or undefined for a failure that carries none. */
const codeOf = (error: unknown): string | undefined =>
  error instanceof ApiError ? error.code : undefined;

export class PowerControl {
  #phase = $state<Phase>('loading');
  #on = $state(false);
  #available = $state(true);
  #busy = $state(false);
  #failure = $state<string | undefined>();
  #loadFailure = $state<string | undefined>();

  readonly #service: PowerService;
  readonly #deviceId: string;
  readonly #signal: AbortSignal;

  constructor(service: PowerService, deviceId: string, signal: AbortSignal) {
    this.#service = service;
    this.#deviceId = deviceId;
    this.#signal = signal;
  }

  // Everything below is read-only from outside by design: the only writers
  // are `load` and `switchTo`, which are the two things the server answers.
  get phase(): Phase {
    return this.#phase;
  }
  get on(): boolean {
    return this.#on;
  }
  /** False when Home Assistant says the plug is unavailable. */
  get available(): boolean {
    return this.#available;
  }
  /** True while a switch is in flight. */
  get busy(): boolean {
    return this.#busy;
  }
  /** The code of the refusal that stopped the last switch, or the load. */
  get failure(): string | undefined {
    return this.#failure ?? this.#loadFailure;
  }

  /** Reads the plug. A printer with no plug is a state, not a fault. */
  async load(): Promise<void> {
    this.#phase = 'loading';
    this.#loadFailure = undefined;

    try {
      const held = await this.#service.get(this.#deviceId, this.#signal);
      this.#on = held.on;
      this.#available = held.available;
      this.#phase = 'ready';
    } catch (error) {
      if (this.#signal.aborted) return;

      if (codeOf(error) === 'ha-power/NOT_MAPPED') {
        this.#phase = 'not-mapped';

        return;
      }

      this.#loadFailure = codeOf(error) ?? 'other';
      this.#phase = 'error';
    }
  }

  /**
   * Asks for the plug to take a state. The shown state moves only when the
   * server accepted, so a refusal leaves the toggle where the plug is.
   */
  async switchTo(on: boolean): Promise<void> {
    if (this.#busy || this.#phase !== 'ready') return;

    this.#busy = true;
    this.#failure = undefined;

    try {
      this.#on = await this.#service.set(this.#deviceId, on, this.#signal);
    } catch (error) {
      if (this.#signal.aborted) return;

      this.#failure = codeOf(error) ?? 'other';
    } finally {
      this.#busy = false;
    }
  }
}
