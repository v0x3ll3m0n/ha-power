// The service layer: the module's own routes, reached through the host's
// client (svelte-data.md, frontend-structure.md).
//
// `bind` comes from `ha-power-client`, which `cmd/generate-client-sdk -module`
// wrote from examples/ha-power/openapi.json. The client the host hands over is
// in session mode, so the credential is the cookie the browser already holds.
// Nothing here names a URL, a header or a token: the Home Assistant token
// lives in the Go process and never reaches the page.
import type { ModuleHost } from '@prusa/connect-ui/slots';
import { bind, type components } from 'ha-power-client';

/** What Home Assistant says about one printer's plug. */
export type PowerState = components['schemas']['PowerState'];

/** What the store needs of the module's routes. A test hands in a fake. */
export interface PowerService {
  get(deviceId: string, signal?: AbortSignal): Promise<PowerState>;
  set(deviceId: string, on: boolean, signal?: AbortSignal): Promise<boolean>;
}

/** The two routes, bound onto the client the host handed this module. */
export const powerOn = (host: ModuleHost): PowerService => {
  const routes = bind(host.client);

  return {
    get: (deviceId, signal) =>
      routes.getPower({ path: { device: deviceId } }, signal),
    set: async (deviceId, on, signal) =>
      (
        await routes.setPower(
          { path: { device: deviceId }, body: { on } },
          signal,
        )
      ).on,
  };
};
