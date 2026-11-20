// Command ha-power is a module with both halves: this process, which talks to
// Home Assistant and serves two routes, and a browser bundle under ui/, which
// draws a power toggle on them.
//
// It turns each printer's smart plug on and off through a Home Assistant
// switch.* entity. The browser cannot call Home Assistant: the page's content
// security policy allows only its own origin, and a bundle gets no fetch. So
// every call goes through this process, and the long-lived access token lives
// here and never leaves the server.
//
// # The two routes
//
//	GET  /power/{device}   the plug's state, read from Home Assistant
//	POST /power/{device}   turn the plug on or off, with the body {"on": bool}
//
// Both are relative to the mount, so a caller reaches them at
// /api/v1/modules/<namespace>/power/{device}. The host decides who may call
// each one and then makes the call: view_resource on the instance for the read,
// operate_resource for the write. This process checks no permission of its own
// on the instance.
//
// # What a caller may name
//
// A caller names a printer and never an entity. The entity comes from the
// switches setting the operator wrote, so a module that can be called by every
// signed-in person still switches nothing but the plugs the operator listed.
//
// # The guard on power-off
//
// Before it turns a plug off, the process reads the printer as the person who
// asked and refuses while the printer prints, is paused or busy, or has a
// nozzle above 50 degrees. The page makes the same check, to disable the
// button, but a page is only a convenience: this is the check that holds.
//
// # What an operator writes
//
// ha_url, ha_token and switches, on the module's page under Settings,
// Extensions. See examples/ha-power/README.md.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// What this child reports when the host asks it what it is.
const (
	name    = "ha-power"
	release = extension.Version("0.1.0")
)

// openAPI is the fragment describing the two routes, with every path relative
// to the mount. The host holds it to routesapi.ParseFragment at launch and
// serves it rebased under the namespace, which is what cmd/generate-client-sdk
// reads to type this module's surface.
//
//go:embed openapi.json
var openAPI []byte

// haTimeout is how long one call to Home Assistant may take.
const haTimeout = 5 * time.Second

// maxHAAnswer bounds what is read back from Home Assistant. A state or a
// service answer is a few hundred bytes.
const maxHAAnswer = 1 << 20

// maxBody bounds a request body to this module. The body is {"on": true}.
const maxBody = 1 << 10

// The state values and the paths in a printer's state tree this module reads.
// The tree is the one nozzle-watch reads (examples/nozzle-watch/ui/src/nozzle.ts).
const (
	statePath  = "/state"
	nozzlePath = "/tools/0/nozzle_temp"

	// maxNozzleTemp is the hottest nozzle, in degrees Celsius, the module
	// powers off under. A hot nozzle keeps heat in the block while the fan that
	// cools it stops with the power.
	maxNozzleTemp = 50.0
)

// busyStates are the printer states the module will not power off in.
//
//nolint:gochecknoglobals // a closed table read on every power-off, never written.
var busyStates = map[string]bool{"PRINTING": true, "PAUSED": true, "BUSY": true}

// entityPattern is the shape of a Home Assistant switch entity. It is checked
// when the settings arrive, so a mapping that is not a switch refuses the start
// instead of reaching a service call.
var entityPattern = regexp.MustCompile(`^switch\.[a-z0-9_]+$`)

// Settings is what an operator writes under this instance's config block. The
// host merges it over the defaults module.yaml declares and sends the result on
// plugin.configure, before any request reaches this module.
type Settings struct {
	// HAURL is the base address of Home Assistant.
	HAURL string `json:"ha_url"`
	// HAToken is the long-lived access token. It is a secret: it is never
	// logged and never put in an answer.
	HAToken string `json:"ha_token"`
	// Switches maps a printer id to the Home Assistant entity that powers it.
	Switches map[string]string `json:"switches"`
}

// errSettings reports a settings value this module will not run with. The
// operator reads the reason on the instance.
var errSettings = errors.New("the settings are not usable")

// checked trims the settings and refuses what cannot work, so a mistake is
// found at the start and not on the first click.
func (s Settings) checked() (Settings, error) {
	held := Settings{
		HAURL:    strings.TrimRight(strings.TrimSpace(s.HAURL), "/"),
		HAToken:  strings.TrimSpace(s.HAToken),
		Switches: make(map[string]string, len(s.Switches)),
	}

	address, err := url.Parse(held.HAURL)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
		return Settings{}, fmt.Errorf("%w: ha_url must be an http or https address, such as http://homeassistant.local:8123", errSettings)
	}

	if held.HAToken == "" {
		return Settings{}, fmt.Errorf("%w: ha_token is empty", errSettings)
	}

	for device, entity := range s.Switches {
		entity = strings.TrimSpace(entity)
		if strings.TrimSpace(device) == "" || !entityPattern.MatchString(entity) {
			return Settings{}, fmt.Errorf("%w: switches maps %q to %q, and the entity must look like switch.mk4_plug",
				errSettings, device, entity)
		}

		held.Switches[strings.TrimSpace(device)] = entity
	}

	return held, nil
}

// settingsHolder keeps the settings the host last sent. Configure can arrive
// again while requests are being served, so every read takes the lock.
type settingsHolder struct {
	mu   sync.RWMutex
	held Settings
}

func (h *settingsHolder) set(s Settings) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.held = s
}

// entityOf answers the entity a printer is mapped to, with the address and
// token to reach Home Assistant, as one consistent snapshot.
func (h *settingsHolder) entityOf(device string) (entity, base, token string, mapped bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	entity, mapped = h.held.Switches[device]

	return entity, h.held.HAURL, h.held.HAToken, mapped
}

// fleet is the one call this module makes on the host's fleet. A test hands in
// a fake.
type fleet interface {
	Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error)
}

// service is everything the routes need.
type service struct {
	settings settingsHolder
	fleet    fleet
	http     *http.Client
}

func newService(devices fleet, timeout time.Duration) *service {
	return &service{
		fleet: devices,
		http: &http.Client{
			Timeout: timeout,
			// A redirect is never followed: the token rides on this request, and
			// a Home Assistant that redirects has been put behind something the
			// operator did not name here.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

// run writes the handshake line and then serves frames until the host closes
// the channel.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("render the handshake: %w", err)
	}

	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write the handshake: %w", err)
	}

	serves := wirerpc.Methods{}

	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{Role: wirerpc.RoleChild, Methods: serves})
	if err != nil {
		return fmt.Errorf("build the session: %w", err)
	}

	held := newService(deviceapi.NewClient(conn), haTimeout)

	for method, handler := range methods(held) {
		serves[method] = handler
	}

	fmt.Fprintf(os.Stderr, "%s %s is serving on stdio as pid %d\n", name, release, os.Getpid())

	if err = conn.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// methods is the lifecycle set every child answers, plus the one call that
// carries this module's own HTTP surface.
func methods(held *service) wirerpc.Methods {
	return wirerpc.Methods{
		wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
			return routesapi.Describe(
				wirerpc.Info{
					Name:     name,
					Version:  release,
					Contract: wirerpc.Contract,
					// This module reads three settings, so the host sends them.
					Serves: []wirerpc.Method{wirerpc.MethodConfigure},
				}, openAPI)
		},
		wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
			fmt.Fprintf(os.Stderr, "%s was asked to stop\n", name)

			return nil, nil
		},
		wirerpc.MethodConfigure: wirerpc.Configure(func(_ context.Context, settings Settings) error {
			checked, err := settings.checked()
			if err != nil {
				return err
			}

			held.settings.set(checked)

			return nil
		}),
		routesapi.MethodServe: routesapi.Serve(routes(held)),
	}
}

// routes is the surface this module serves. The paths carry no prefix:
// /power/{device} here is /api/v1/modules/<namespace>/power/{device} to a
// caller.
func routes(held *service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /power/{device}", held.read)
	mux.HandleFunc("POST /power/{device}", held.write)

	// Everything else is the module's own 404, and the path it reports is what
	// proves the group prefix was taken off before it arrived.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		answer(w, http.StatusNotFound, map[string]any{"module_saw": r.URL.Path})
	})

	return mux
}

// PowerState is the answer of GET /power/{device}.
type PowerState struct {
	On        bool   `json:"on"`
	Available bool   `json:"available"`
	Entity    string `json:"entity"`
}

// read answers what Home Assistant says about one printer's plug.
func (s *service) read(w http.ResponseWriter, r *http.Request) {
	entity, base, token, mapped := s.settings.entityOf(r.PathValue("device"))
	if !mapped {
		refuse(w, errNotMapped)

		return
	}

	var held struct {
		State string `json:"state"`
	}

	if err := s.callHA(r.Context(), http.MethodGet, base+"/api/states/"+url.PathEscape(entity), token, nil, &held); err != nil {
		refuse(w, err)

		return
	}

	answer(w, http.StatusOK, PowerState{
		On:        held.State == "on",
		Available: held.State != "unavailable",
		Entity:    entity,
	})
}

// write turns one printer's plug on or off.
func (s *service) write(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("device")

	// The entity is looked up from the operator's mapping and from nothing the
	// caller sent. The body carries one bool and no entity id, and a body that
	// carries any other key is refused.
	entity, base, token, mapped := s.settings.entityOf(device)
	if !mapped {
		refuse(w, errNotMapped)

		return
	}

	on, err := wantedOf(r.Body)
	if err != nil {
		refuse(w, err)

		return
	}

	if err = s.mayPower(r, device, on); err != nil {
		refuse(w, err)

		return
	}

	service := "turn_off"
	if on {
		service = "turn_on"
	}

	call, err := json.Marshal(map[string]string{"entity_id": entity})
	if err != nil {
		refuse(w, fmt.Errorf("render the call: %w", err))

		return
	}

	if err = s.callHA(r.Context(), http.MethodPost, base+"/api/services/switch/"+service, token, call, nil); err != nil {
		refuse(w, err)

		return
	}

	answer(w, http.StatusOK, map[string]bool{"on": on})
}

// wantedOf reads {"on": bool}. The field is required, so a body that forgot it
// is not read as a request to turn the plug off.
func wantedOf(body io.Reader) (bool, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxBody))
	if err != nil {
		return false, fmt.Errorf("%w: %w", errBadBody, err)
	}

	var held struct {
		On *bool `json:"on"`
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(&held); err != nil {
		return false, fmt.Errorf("%w: %w", errBadBody, err)
	}

	if held.On == nil {
		return false, fmt.Errorf("%w: on is missing", errBadBody)
	}

	return *held.On, nil
}

// mayPower reads the printer as the person who asked. Every power change needs
// the printer to be one that person may see. Turning off also needs it to be
// at rest and cool.
func (s *service) mayPower(r *http.Request, device string, on bool) error {
	printer, err := s.fleet.Get(r.Context(), deviceapi.GetParams{
		Principal: routesapi.PrincipalOf(r),
		ID:        device,
	})

	switch {
	case errors.Is(err, deviceapi.ErrNoDevice), errors.Is(err, deviceapi.ErrDenied):
		return errNoDevice
	case on:
		// Turning on harms nothing, so a fleet that cannot be read does not
		// stop it: a person with a dead printer has to be able to power it up.
		return nil
	case err != nil:
		// Turning off is the guarded move, and a state that cannot be read
		// cannot be shown safe.
		return fmt.Errorf("%w: %w", errStateUnreadable, err)
	}

	return powerOffRefusal(printer)
}

// powerOffRefusal answers why a printer must not lose power now, or nil. It
// reads the last state the printer reported, which for a printer that has gone
// away is the state it was in when it went. That is on purpose: a printer
// whose link dropped mid-print is still printing.
func powerOffRefusal(printer deviceapi.Device) error {
	var state string
	if raw, ok := printer.State[statePath]; ok && json.Unmarshal(raw, &state) == nil && busyStates[state] {
		return fmt.Errorf("%w: the printer is %s", errPrinterBusy, state)
	}

	var nozzle float64
	if raw, ok := printer.State[nozzlePath]; ok && json.Unmarshal(raw, &nozzle) == nil && nozzle > maxNozzleTemp {
		return fmt.Errorf("%w: the nozzle is %.0f degrees, and it must be %.0f or less", errNozzleHot, nozzle, maxNozzleTemp)
	}

	return nil
}

// callHA makes one call to Home Assistant. It answers an error already shaped
// as a refusal: the detail never holds the token, and a network error is
// reduced to what failed, because the library's text for it can carry the
// address.
func (s *service) callHA(ctx context.Context, method, address, token string, body []byte, into any) error {
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: the Home Assistant address cannot be used", errHAUnreachable)
	}

	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := s.http.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%w: Home Assistant did not answer in time", errHAUnreachable)
		}

		return fmt.Errorf("%w: the request to Home Assistant failed", errHAUnreachable)
	}

	defer response.Body.Close()

	// The body is read in full even when it is not wanted, so the connection
	// goes back to the pool.
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxHAAnswer))
	if err != nil {
		return fmt.Errorf("%w: the answer of Home Assistant was cut off", errHAUnreachable)
	}

	switch {
	case response.StatusCode == http.StatusUnauthorized, response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: Home Assistant answered %d", errHAAuth, response.StatusCode)
	case response.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: Home Assistant answered 404", errHANoEntity)
	case response.StatusCode < 200 || response.StatusCode > 299:
		return fmt.Errorf("%w: Home Assistant answered %d", errHAFailed, response.StatusCode)
	}

	if into == nil {
		return nil
	}

	if err = json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: Home Assistant answered with something that is not a state", errHAFailed)
	}

	return nil
}

// The refusals this module makes, each with its own status and code in refuse.
var (
	errNotMapped       = errors.New("this printer has no plug in the switches setting")
	errNoDevice        = errors.New("this printer is not one you may switch")
	errBadBody         = errors.New("the body must be {\"on\": true} or {\"on\": false}")
	errPrinterBusy     = errors.New("the printer is not at rest")
	errNozzleHot       = errors.New("the nozzle is hot")
	errStateUnreadable = errors.New("the state of the printer could not be read")
	errHAUnreachable   = errors.New("Home Assistant could not be reached")
	errHAAuth          = errors.New("Home Assistant refused the token")
	errHANoEntity      = errors.New("Home Assistant has no such entity")
	errHAFailed        = errors.New("Home Assistant failed")
)

// refuse answers one refusal as RFC 9457 names it, which is the shape the
// client SDK reads: a caller gets the module's own code on ApiError.code and
// the sentence on ApiError.message.
//
// The codes are this module's own, namespaced by its short name.
func refuse(w http.ResponseWriter, err error) {
	status, code, title, retry := http.StatusInternalServerError, "ha-power/INTERNAL", "the module failed", false

	switch {
	case errors.Is(err, errNotMapped):
		status, code, title = http.StatusNotFound, "ha-power/NOT_MAPPED", "the printer has no plug"
	case errors.Is(err, errNoDevice):
		status, code, title = http.StatusNotFound, "ha-power/NO_DEVICE", "no such printer"
	case errors.Is(err, errBadBody):
		status, code, title = http.StatusBadRequest, "ha-power/INVALID_BODY", "the request was not understood"
	case errors.Is(err, errPrinterBusy):
		status, code, title = http.StatusConflict, "ha-power/PRINTER_BUSY", "the printer is in use"
	case errors.Is(err, errNozzleHot):
		status, code, title = http.StatusConflict, "ha-power/NOZZLE_HOT", "the nozzle is still hot"
	case errors.Is(err, errStateUnreadable):
		status, code, title, retry = http.StatusServiceUnavailable, "ha-power/STATE_UNREADABLE", "the printer state is unknown", true
	case errors.Is(err, errHAUnreachable):
		status, code, title, retry = http.StatusBadGateway, "ha-power/HA_UNREACHABLE", "Home Assistant is not reachable", true
	case errors.Is(err, errHAAuth):
		status, code, title = http.StatusBadGateway, "ha-power/HA_AUTH", "Home Assistant refused the token"
	case errors.Is(err, errHANoEntity):
		status, code, title = http.StatusBadGateway, "ha-power/HA_NO_ENTITY", "Home Assistant has no such plug"
	case errors.Is(err, errHAFailed):
		status, code, title, retry = http.StatusBadGateway, "ha-power/HA_FAILED", "Home Assistant failed", true
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":      "about:blank",
		"status":    status,
		"code":      code,
		"title":     title,
		"detail":    err.Error(),
		"params":    map[string]any{},
		"retryable": retry,
	})
}

// answer writes one JSON body, which is every success above.
func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}

// stdio is the pair the host handed over. Close shuts the reading half, which
// is what ends a session whose host has gone quiet.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
