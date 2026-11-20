# The ha-power module

A module with both halves that turns each printer's smart plug on and off
through a Home Assistant `switch.*` entity.

- The Go process holds the Home Assistant address and token, calls the Home
  Assistant REST API and serves two routes.
- The Svelte bundle draws a power switch on each printer's card and on its
  Status tab. It calls the module's routes through `host.client`, which uses the
  browser's session cookie. The page holds no token.

The browser cannot call Home Assistant itself. The page's content security
policy is `connect-src 'self'` and a bundle gets no `fetch`, so every call goes
through the Go process. The token stays on the Pi.

It is a standalone repository on the Prusa Connect extension template. Its module
path is `github.com/v0x3ll3m0n/ha-power`, so its id is
`com.github.aux.v0x3ll3m0n.ha-power`.

## The routes

Both are served at `/api/v1/modules/<namespace>/...`. The host takes
`view_resource` on the module instance before the read and `operate_resource`
before the write.

| route | does |
| --- | --- |
| `GET /power/{device}` | reads the plug from Home Assistant and answers `{on, available, entity}` |
| `POST /power/{device}` with `{"on": bool}` | calls `switch.turn_on` or `switch.turn_off` for the mapped entity and answers `{on}` |

A caller names a printer and never an entity. The entity comes from the
`switches` setting, and a body with any key but `on` is refused.

Home Assistant is called with `Authorization: Bearer <token>`, a 5 second
timeout and no redirects. A failure becomes an RFC 9457 refusal with one of
these codes:

| code | status | meaning |
| --- | --- | --- |
| `ha-power/NOT_MAPPED` | 404 | the printer has no entry in `switches` |
| `ha-power/NO_DEVICE` | 404 | the person may not see that printer |
| `ha-power/INVALID_BODY` | 400 | the body is not `{"on": true}` or `{"on": false}` |
| `ha-power/PRINTER_BUSY` | 409 | power off while the printer is PRINTING, PAUSED or BUSY |
| `ha-power/NOZZLE_HOT` | 409 | power off while the nozzle is above 50 degrees |
| `ha-power/STATE_UNREADABLE` | 503 | power off, and the printer's state could not be read |
| `ha-power/HA_UNREACHABLE` | 502 | no answer from Home Assistant, or no answer in 5 seconds |
| `ha-power/HA_AUTH` | 502 | Home Assistant answered 401 or 403: the token is wrong |
| `ha-power/HA_NO_ENTITY` | 502 | Home Assistant answered 404: the entity does not exist |
| `ha-power/HA_FAILED` | 502 | any other answer that is not 2xx |

## The guard on power off

The SDK lets a module read a device (`deviceapi.Client.Get`), so the guard is
in the Go half and does not depend on the page. Before it turns a plug off, the
process reads the printer **as the person who asked** and refuses when:

- `/state` is `PRINTING`, `PAUSED` or `BUSY`, or
- `/tools/0/nozzle_temp` is above 50 degrees, or
- the state cannot be read (fail closed).

The page makes the same check from `device.state` and disables the switch with
the reason, but that is a convenience. Turning on is never refused for these
reasons.

Because the read is made as the person, a person who may not see a printer
cannot switch its plug, even when they may operate the module. The manifest
therefore has no `permissions` key: the module takes nothing as itself.

There are two limits. The guard reads the **last state the printer reported**,
including for a printer that has dropped off the network. A printer whose link
broke in the middle of a print is still treated as printing, and a hot nozzle
that was last seen hot stays blocked. To cut power regardless, use Home
Assistant directly. And only the first tool, `/tools/0`, is checked.

## Build it

Needs Go, Node 22.12 or later and pnpm 11. Both halves build against a checkout of
[Prusa Connect](https://github.com/prusa3d/prusa-connect) at `CORE_REF` in the
`Makefile`, which `make` clones into `.core/` until the SDK and the `@prusa` npm
packages are published. `go.mod` has a `replace` line for the same reason.

```bash
make check     # Go vet and tests, the client is current, the UI's lint, types, tests and build
make release   # release/: module.yaml, ui/, one binary per platform in module.yaml
make install   # installs release/ into bin/try with the pinned core's own `module add`
```

`make check` runs the component tests in Chromium and installs it with
`pnpm exec playwright install chromium`.

After you change `openapi.json`, run `make client` and commit what it writes.

`license` in `module.yaml` is the same as Prusa Connect's, and `LICENSE` holds its text.

## Install it

Copy the `release/` directory to the machine that runs Prusa Connect, then:

```bash
prusa-connect module add github.com/v0x3ll3m0n/ha-power@0.1.0 -from <release-dir>
```

Without `-approve` the declaration is written switched off and the command
prints what the module asks for, including the egress to your Home Assistant on
port 8123. Read it, then run the same line again with `-approve`, and restart the
server so it reads the change. Then open the module's Settings tab under
Settings, Extensions and write `ha_url`, `ha_token` and `switches`. The module
does not start until `ha_url` and `ha_token` are set.

### On Docker

The command is part of the server binary, so run it in the container. Put the
release where the container sees it, for example in a folder mounted at `/tmp`,
and use absolute paths. The container's working directory is `/`, and a relative
path then points at the read-only root filesystem.

```bash
docker exec prusa-connect /prusa_connect module add github.com/v0x3ll3m0n/ha-power@0.1.0 -config /config/config.yaml -from /tmp/ha-power -approve
docker restart prusa-connect
```

`/config/config.d` must be writable by the container's user, uid 10001. Do not
build the archive on macOS without `COPYFILE_DISABLE=1`: its `tar` adds `._*`
files that the installer refuses.

## Set up Home Assistant

1. **Make a dedicated user.** In Home Assistant, Settings, People, Users, add
   a user such as `prusa-connect`. Leave "Administrator" **off**.
2. **Generate the token** while signed in as that user: Profile, Security,
   Long-lived access tokens, Create token. Copy it at once, because Home
   Assistant shows it once. Write it into `ha_token`. It is masked after you
   save.
3. **Name the plugs.** Each plug is a `switch.*` entity. Find its id in
   Settings, Devices & services, Entities (for example `switch.mk4_plug`).
4. **Find each printer's device id.** On the Pi, with the Prusa Connect API
   reachable, list the printers and read the `id` field of each:

   ```bash
   curl -s -b cookies.txt http://<pi>:8080/api/v1/devices
   ```

5. **Fill in `switches`** as JSON, one line per printer:

   ```json
   {
     "p3f2a1c9b": "switch.mk4_plug",
     "p8c40d7e1": "switch.core_one_plug"
   }
   ```

   An unlisted printer shows "No plug is set for this printer". An
   entity that does not look like `switch.<name>` refuses the start of the
   module, and the reason is shown on the instance.

A non-admin Home Assistant user can still call any service on any entity its
token reaches, so the dedicated user limits who holds the token, not what it
can do. Home Assistant has no per-entity token scope. This module only ever
sends the entities in `switches`.

## Optional: switch the plug off after a print

Prusa Connect publishes a printer's state on its MQTT sink, on
`v1/devices/printers/{id}/data/state`. This Home Assistant automation turns the
plug off when the print is FINISHED and the nozzle has cooled. It uses the MQTT
integration, pointed at the Prusa Connect broker. Check the payload your server
publishes on that topic first (for example with `mosquitto_sub`) and adjust the
two templates to its shape.

```yaml
alias: Printer plug off after print
mode: single
trigger:
  - platform: mqtt
    topic: v1/devices/printers/p3f2a1c9b/data/state
condition:
  - condition: template
    value_template: "{{ trigger.payload_json.value == 'FINISHED' }}"
action:
  # Wait for the nozzle to cool. It reads the same state tree through the
  # temperature topic, or a sensor you already have for the printer.
  - wait_template: "{{ states('sensor.mk4_nozzle_temperature') | float(999) <= 50 }}"
    timeout: "01:00:00"
    continue_on_timeout: false
  - service: switch.turn_off
    target:
      entity_id: switch.mk4_plug
```

This automation talks to Home Assistant directly, so it does not pass through
the module's guard. Its own conditions are the guard.

## Publish

1. Raise `version` in `module.yaml` and add its heading to `CHANGELOG.md`.
2. Publish a GitHub release tagged `v<version>`. A pre-release goes to the beta
   channel, and the release notes become the changelog on the marketplace.
3. `.github/workflows/release.yml` calls the marketplace's builder, which builds
   every platform and the bundle from the commit, signs the release and uploads
   it with the source. A person at Prusa reviews it before operators can install
   it.

One-time setup:

| what | where |
| --- | --- |
| the repository is public, because GitHub records the build attestation only for a public repository | repository settings, General |
| an author profile and the public half of your signing key | marketplace, Become an author, Signing keys |
| an environment named `release` that only `v*` tags may deploy to, with required reviewers | repository settings, Environments |
| `PRUSA_CONNECT_EXTENSION_SIGNING_KEY`: the RSA private key whose public half is registered | secret of that environment |
| a trusted publisher: the id `com.github.aux.v0x3ll3m0n.ha-power`, `v0x3ll3m0n/ha-power`, `release.yml` and the environment | marketplace author console, Trusted publishing |

The marketplace documents each step in
[publishing an extension from GitHub Actions](https://plugin-marketplace.prusa3d.com/docs/connect/github-actions).

## Key files

| path | purpose |
| --- | --- |
| `main.go` | the Go half: handshake, settings, the two routes, the guard |
| `openapi.json` | the fragment describing the routes |
| `module.yaml` | the manifest: options, egress, the `ui` block |
| `routes_test.go` | the routes driven as `routes.serve` calls, with Home Assistant faked by `httptest.Server` |
| `manifest_test.go` | the manifest and fragment read the way the host reads them |
| `client/` | the typed client written by `make client` |
| `ui/src/index.ts` | the bundle's entry: registers the card line and the section |
| `ui/src/power.ts` | the module's routes bound onto `host.client` |
| `ui/src/safety.ts` | the power off lock read from `device.state` |
| `ui/src/power-control.svelte.ts` | the state of one printer's control |
| `ui/src/power-control/` | the switch, with its loading, not-mapped, unavailable and error states |
| `ui/src/locales/en.json` | every text the module shows |

## Not included

- A `device_action` menu item. The switch on the card and the section covers it.
- Writes under another person's name. Every call is made as the person who is
  using the page.
- This is a public beta SDK. The extension API can change before 1.0.
