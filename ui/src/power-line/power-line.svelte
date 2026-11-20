<script lang="ts">
  import { PowerControl } from '../power-control';

  interface Props {
    /** The printer this line is about. It is all a device-card line is handed (ADR-20260911-4p6 rule 10). */
    deviceId: string;
  }

  let { deviceId }: Props = $props();
</script>

<PowerControl {deviceId} compact />
