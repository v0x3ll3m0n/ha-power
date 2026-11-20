<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Button,
    Switch,
  } from '@prusa/connect-ui/primitives';
  import { untrack } from 'svelte';

  import { heldHost } from '../host';
  import { powerOn } from '../power';
  import { PowerControl } from '../power-control.svelte';
  import { lockOf, NOZZLE_LIMIT } from '../safety';

  interface Props {
    /** The printer this control is about. It is all a slot is handed (ADR-20260911-4p6 rule 10). */
    deviceId: string;
    /** One line, for the card in the listing. The section draws the reason below the toggle. */
    compact?: boolean;
  }

  let { deviceId, compact = false }: Props = $props();

  // Every text this module draws comes from its own catalogue, through the
  // `t` the host bound to this module's namespace (frontend-i18n.md rule 7).
  const host = heldHost();
  const t = host.t;

  // One control per mounted component, so two cards side by side never share
  // a plug. `untrack` says the printer id is read once: a card is mounted for
  // one printer.
  const control = new PowerControl(
    powerOn(host),
    untrack(() => deviceId),
    host.signal,
  );
  void control.load();

  const device = $derived(host.fleet.device(deviceId));
  const lock = $derived(lockOf(device));

  // Power off is the move the lock stops. Turning on is never locked.
  const blocked = $derived(control.on && lock !== undefined);
  const disabled = $derived(control.busy || !control.available || blocked);

  const stateText = $derived(control.on ? t('power.on') : t('power.off'));

  // The one sentence that explains why the toggle does nothing, if any.
  const reason = $derived.by(() => {
    if (control.phase !== 'ready') return undefined;
    if (!control.available) return t('power.unavailable');
    if (blocked && lock !== undefined) {
      return t(`power.lock.${lock}`, { limit: `${String(NOZZLE_LIMIT)}°C` });
    }

    return undefined;
  });

  const failureText = $derived(
    control.failure === undefined
      ? undefined
      : t(
          control.failure === 'other'
            ? 'power.error.other'
            : `power.error.${control.failure}`,
        ),
  );

  // A card in the listing is a link. The switch is not part of it.
  const stop = (event: Event) => {
    event.stopPropagation();
  };
</script>

<div
  data-part="power"
  class={[
    'flex flex-wrap items-center gap-x-3 gap-y-1',
    compact ? 'text-sm' : 'text-base',
  ]}
>
  {#if control.phase === 'loading'}
    <span data-part="loading" class="text-muted-foreground">
      {t('power.loading')}
    </span>
  {:else if control.phase === 'not-mapped'}
    <span data-part="not-mapped" class="text-muted-foreground">
      {t('power.notMapped')}
    </span>
  {:else if control.phase === 'error'}
    <Alert variant="destructive" data-part="load-error">
      <AlertDescription>{failureText}</AlertDescription>
    </Alert>
    <Button
      variant="link"
      data-part="reload"
      onclick={(event: MouseEvent) => {
        event.preventDefault();
        event.stopPropagation();
        void control.load();
      }}
    >
      {t('power.reload')}
    </Button>
  {:else}
    <!-- The switch shows the plug and never its own guess: a change is a
         request, and the shown state moves only when the module accepted it,
         so a refusal leaves the switch where the plug is. -->
    <Switch
      bind:checked={
        () => control.on, (next: boolean) => void control.switchTo(next)
      }
      aria-label={t('power.toggle')}
      data-part="toggle"
      {disabled}
      onclick={stop}
    />

    <!-- The state changes under the reader, so the region announces itself
         rather than changing in silence (svelte-5.md). -->
    <span data-part="state" aria-live="polite" class="font-medium">
      {control.busy ? t('power.switching') : stateText}
    </span>

    {#if reason !== undefined}
      <span
        data-part="reason"
        class="text-sm text-muted-foreground"
        title={compact ? reason : undefined}
      >
        {reason}
      </span>
    {/if}

    {#if failureText !== undefined}
      <Alert variant="destructive" data-part="failure">
        <AlertDescription>{failureText}</AlertDescription>
      </Alert>
    {/if}
  {/if}
</div>
