// The bundle's entry. The app imports this file and calls what it default
// exports, handing it the one object a module receives (ADR-20260911-4p6 rule 18).
import { defineModule } from '@prusa/connect-ui/slots';

import { holdHost } from './host';
import { PowerLine } from './power-line';
import { PowerSection } from './power-section';
import './styles.css';

export default defineModule((host) => {
  // The components read the host and the fleet off it, and the host arrives
  // before any component is mounted, so it is held here.
  holdHost(host);

  // The two, and only the two, the manifest declares. An id `contributes` does
  // not carry is refused and this module is dropped whole (ADR-20260911-4p6
  // rule 21).
  host.register.deviceCard('power', PowerLine);
  host.register.deviceSection('power', PowerSection);
});
