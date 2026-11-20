<script lang="ts">
  import { PowerControl } from '../power-control';

  interface Props {
    /** The printer this section is about. It is all a section is handed (ADR-20260911-4p6 rule 10). */
    deviceId: string;
  }

  let { deviceId }: Props = $props();
</script>

<!-- No heading here. The app draws one from the manifest's section title. -->
<PowerControl {deviceId} />
