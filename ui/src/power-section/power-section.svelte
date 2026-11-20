<script lang="ts">
  import { heldHost } from '../host';
  import { PowerControl } from '../power-control';

  interface Props {
    /** The printer this section is about. It is all a section is handed (ADR-20260911-4p6 rule 10). */
    deviceId: string;
  }

  let { deviceId }: Props = $props();

  const t = heldHost().t;
</script>

<section class="flex flex-col gap-2">
  <h3 class="text-sm font-medium text-muted-foreground">{t('power.label')}</h3>
  <PowerControl {deviceId} />
</section>
