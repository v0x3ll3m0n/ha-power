// The host object this module was handed, held for the components to read.
//
// A module-level holder and not Svelte context, for the reason
// examples/spools/ui/src/shelf.svelte.ts gives: the setup function runs before
// any component of this module is mounted, so there is no component for
// `setContext` to hang on. It is set once, in `index.ts`, before a section can
// be drawn: a bundle is imported once per page, and a module is registered
// once per import (ADR-20260911-4p6 rule 25).
//
// It is a plain `.ts` file and not a `.svelte.ts` one. Nothing here is
// reactive: the host object never changes, and what does change is inside
// `host.fleet`, which is the library's own store and already a rune
// (svelte-5.md).
import type { ModuleHost } from '@prusa/connect-ui/slots';

let held: ModuleHost | undefined;

/**
 * Called once by the module's setup function, with the host it was given. The
 * host belongs to the person who admitted this module. When its signal fires,
 * that person has left, so the host is let go and the next setup holds a new
 * one.
 */
export const holdHost = (host: ModuleHost): void => {
  // A setup that was still running when the person left resumes with a
  // signal that already fired. Its host is not held.
  if (host.signal.aborted) return;
  held = host;
  host.signal.addEventListener(
    'abort',
    () => {
      if (held === host) held = undefined;
    },
    { once: true },
  );
};

/** The host the components read. It throws rather than drawing an empty card. */
export const heldHost = (): ModuleHost => {
  if (!held) {
    throw new Error(
      'the ha-power module drew a component before its setup ran',
    );
  }

  return held;
};
