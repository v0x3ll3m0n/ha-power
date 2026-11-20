# Changelog

What an operator sees change when they upgrade this module, newest release
first. `version:` in `module.yaml` is the newest heading below.

## 0.1.2 - 2026-11-20

### Fixed

- `POST /power/{device}` now asks the host whether the person may operate the
  printer, and refuses with 403 `ha-power/NOT_ALLOWED` when they may not. A
  person who could only view a printer could switch its plug before.
- `GET /power/{device}` now asks whether the person may observe the printer.
- A refusal with 503 `ha-power/ACCESS_UNREADABLE` when the host cannot answer.

### Changed

- The egress for `ha_url` lists ports 8123, 443 and 80, so a Home Assistant
  behind a reverse proxy works.
- The setting `ha_url` no longer gives a host name as an example.

## 0.1.1 - 2026-11-20

### Changed

- The module's title is Home Assistant power switch, and its summary says that
  power off is refused during a print or while the nozzle is hot.
- The Status tab section no longer shows the word Power twice.
- The setting `ha_token` no longer says that Home Assistant can limit a token to
  some plugs. It says to create the token for a dedicated user that is not an
  administrator.
- The setting `switches` says that the printer id is the Printer UUID on the
  Printers tab of the web app.

## 0.1.0 - 2026-11-20

### Added

- A Power switch on each printer's card and on its Status tab. It turns the
  printer's Home Assistant plug on and off and shows whether the plug is on.
- `GET /power/{device}` answers the state of a printer's plug.
- `POST /power/{device}` turns the plug on or off.
- The settings `ha_url`, `ha_token` and `switches`. `switches` maps a printer id
  to a Home Assistant `switch.*` entity. A printer that is not listed has no
  switch.
- Power off is refused while the printer is PRINTING, PAUSED or BUSY, or its
  nozzle is above 50 degrees.
