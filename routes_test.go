package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// The routes are driven the way the host drives them, as routes.serve calls,
// with Home Assistant faked by an httptest.Server.

const token = "long-lived-secret-token"

// fakeHA is a Home Assistant that records what it was sent.
type fakeHA struct {
	*httptest.Server

	mu     sync.Mutex
	state  string
	status int
	calls  []haCall
}

type haCall struct {
	Method, Path, Authorization, Body string
}

func newFakeHA(t *testing.T, state string) *fakeHA {
	t.Helper()

	fake := &fakeHA{state: state, status: http.StatusOK}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		fake.mu.Lock()
		defer fake.mu.Unlock()

		fake.calls = append(fake.calls, haCall{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(body)})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fake.status)

		if fake.status == http.StatusOK {
			_, _ = w.Write([]byte(`{"state":"` + fake.state + `","entity_id":"x"}`))
		}
	}))
	t.Cleanup(fake.Close)

	return fake
}

func (f *fakeHA) recorded() []haCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]haCall(nil), f.calls...)
}

// fakeFleet answers one printer, or an error.
type fakeFleet struct {
	state map[string]string
	err   error
	asked []deviceapi.GetParams
}

func (f *fakeFleet) Get(_ context.Context, params deviceapi.GetParams) (deviceapi.Device, error) {
	f.asked = append(f.asked, params)
	if f.err != nil {
		return deviceapi.Device{}, f.err
	}

	tree := map[string]json.RawMessage{}
	for path, value := range f.state {
		tree[path] = json.RawMessage(value)
	}

	return deviceapi.Device{ID: params.ID, State: tree}, nil
}

// rig builds a configured module over a fake Home Assistant.
func rig(t *testing.T, haState string, devices *fakeFleet) (wirerpc.Methods, *fakeHA) {
	t.Helper()

	fake := newFakeHA(t, haState)
	held := newService(devices, time.Second)

	serves := methods(held)
	if err := configure(t, serves, map[string]any{
		"ha_url":   fake.URL + "/",
		"ha_token": token,
		"switches": map[string]string{"printer-1": "switch.mk4_plug"},
	}); err != nil {
		t.Fatalf("the settings were refused: %v", err)
	}

	return serves, fake
}

func configure(t *testing.T, serves wirerpc.Methods, settings map[string]any) error {
	t.Helper()

	// The host sends the settings under one `config` key.
	params, err := json.Marshal(map[string]any{"config": settings})
	if err != nil {
		t.Fatal(err)
	}

	_, err = serves[wirerpc.MethodConfigure](context.Background(), wirerpc.Call{Method: wirerpc.MethodConfigure, Params: params})

	return err
}

func callRoutes(t *testing.T, serves wirerpc.Methods, request routesapi.Request) routesapi.Response {
	t.Helper()

	params, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("render the request: %v", err)
	}

	answered, err := serves[routesapi.MethodServe](context.Background(), wirerpc.Call{Method: routesapi.MethodServe, Params: params})
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.Path, err)
	}

	response, ok := answered.(routesapi.Response)
	if !ok {
		t.Fatalf("the module answered %T", answered)
	}

	if err = response.Validate(); err != nil {
		t.Fatalf("the host would refuse this answer: %v", err)
	}

	return response
}

func get(t *testing.T, serves wirerpc.Methods, device string) routesapi.Response {
	t.Helper()

	return callRoutes(t, serves, routesapi.Request{Method: http.MethodGet, Path: "/power/" + device, Principal: "user:alice"})
}

func post(t *testing.T, serves wirerpc.Methods, device, body string) routesapi.Response {
	t.Helper()

	return callRoutes(t, serves, routesapi.Request{
		Method: http.MethodPost, Path: "/power/" + device, Principal: "user:alice", Body: []byte(body),
	})
}

type refusal struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"`
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()

	var into T
	if err := json.Unmarshal(body, &into); err != nil {
		t.Fatalf("read the answer %q: %v", body, err)
	}

	return into
}

func expectRefusal(t *testing.T, answered routesapi.Response, status int, code string) refusal {
	t.Helper()

	if answered.Status != status {
		t.Fatalf("answered %d and not %d: %s", answered.Status, status, answered.Body)
	}

	said := decode[refusal](t, answered.Body)
	if said.Code != code || said.Status != status || said.Title == "" {
		t.Fatalf("the refusal reads %+v, expected code %s", said, code)
	}

	if strings.Contains(string(answered.Body), token) {
		t.Fatal("the refusal carries the token")
	}

	return said
}

func TestTheReadAnswersWhatHomeAssistantSays(t *testing.T) {
	t.Parallel()

	cases := []struct {
		haState   string
		on, avail bool
	}{
		{"on", true, true},
		{"off", false, true},
		{"unavailable", false, false},
	}

	for _, one := range cases {
		t.Run(one.haState, func(t *testing.T) {
			t.Parallel()

			serves, fake := rig(t, one.haState, &fakeFleet{})

			answered := get(t, serves, "printer-1")
			if answered.Status != http.StatusOK {
				t.Fatalf("the read answered %d: %s", answered.Status, answered.Body)
			}

			power := decode[PowerState](t, answered.Body)
			if power.On != one.on || power.Available != one.avail || power.Entity != "switch.mk4_plug" {
				t.Errorf("the read answered %+v", power)
			}

			calls := fake.recorded()
			if len(calls) != 1 || calls[0].Method != http.MethodGet || calls[0].Path != "/api/states/switch.mk4_plug" {
				t.Errorf("Home Assistant was called as %+v", calls)
			}
		})
	}
}

func TestEveryCallToHomeAssistantCarriesTheToken(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{})

	get(t, serves, "printer-1")
	post(t, serves, "printer-1", `{"on":true}`)

	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("Home Assistant saw %d calls", len(calls))
	}

	for _, call := range calls {
		if call.Authorization != "Bearer "+token {
			t.Errorf("%s %s carried %q", call.Method, call.Path, call.Authorization)
		}
	}
}

func TestAnUnmappedPrinterIsRefusedAndHomeAssistantIsNotCalled(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{})

	expectRefusal(t, get(t, serves, "printer-2"), http.StatusNotFound, "ha-power/NOT_MAPPED")
	expectRefusal(t, post(t, serves, "printer-2", `{"on":false}`), http.StatusNotFound, "ha-power/NOT_MAPPED")

	if calls := fake.recorded(); len(calls) != 0 {
		t.Errorf("Home Assistant was called for an unmapped printer: %+v", calls)
	}
}

func TestTheWriteCallsTheServiceForTheMappedEntity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		body, path string
		on         bool
	}{
		{`{"on":true}`, "/api/services/switch/turn_on", true},
		{`{"on":false}`, "/api/services/switch/turn_off", false},
	}

	for _, one := range cases {
		t.Run(one.path, func(t *testing.T) {
			t.Parallel()

			devices := &fakeFleet{state: map[string]string{"/state": `"IDLE"`, "/tools/0/nozzle_temp": `24.5`}}
			serves, fake := rig(t, "on", devices)

			answered := post(t, serves, "printer-1", one.body)
			if answered.Status != http.StatusOK {
				t.Fatalf("the write answered %d: %s", answered.Status, answered.Body)
			}

			if decode[map[string]bool](t, answered.Body)["on"] != one.on {
				t.Errorf("the write answered %s", answered.Body)
			}

			calls := fake.recorded()
			if len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Path != one.path {
				t.Fatalf("Home Assistant was called as %+v", calls)
			}

			if calls[0].Body != `{"entity_id":"switch.mk4_plug"}` {
				t.Errorf("the service call carried %s", calls[0].Body)
			}

			// The printer is read as the person who asked.
			if len(devices.asked) != 1 || devices.asked[0].Principal != "user:alice" || devices.asked[0].ID != "printer-1" {
				t.Errorf("the fleet was asked %+v", devices.asked)
			}
		})
	}
}

// TestAnEntityTheCallerNamesIsNeverToggled holds the rule that only the
// operator's mapping decides what is switched.
func TestAnEntityTheCallerNamesIsNeverToggled(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{})

	bodies := []string{
		`{"on":false,"entity_id":"switch.oven"}`,
		`{"entity_id":"switch.oven"}`,
		`{"on":false,"entity":"switch.oven"}`,
	}
	for _, body := range bodies {
		expectRefusal(t, post(t, serves, "printer-1", body), http.StatusBadRequest, "ha-power/INVALID_BODY")
	}

	// An entity id in the place of the printer id is no printer at all.
	expectRefusal(t, post(t, serves, "switch.oven", `{"on":false}`), http.StatusNotFound, "ha-power/NOT_MAPPED")

	if calls := fake.recorded(); len(calls) != 0 {
		t.Errorf("Home Assistant was called: %+v", calls)
	}
}

func TestTheWriteRefusesABodyWithNoOnField(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{})

	for _, body := range []string{`{}`, ``, `[true]`, `{"on":"yes"}`} {
		expectRefusal(t, post(t, serves, "printer-1", body), http.StatusBadRequest, "ha-power/INVALID_BODY")
	}

	if calls := fake.recorded(); len(calls) != 0 {
		t.Errorf("Home Assistant was called: %+v", calls)
	}
}

func TestPowerOffIsRefusedWhileThePrinterIsInUseOrHot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		state map[string]string
		code  string
	}{
		{"printing", map[string]string{"/state": `"PRINTING"`}, "ha-power/PRINTER_BUSY"},
		{"paused", map[string]string{"/state": `"PAUSED"`}, "ha-power/PRINTER_BUSY"},
		{"busy", map[string]string{"/state": `"BUSY"`}, "ha-power/PRINTER_BUSY"},
		{"hot", map[string]string{"/state": `"FINISHED"`, "/tools/0/nozzle_temp": `180`}, "ha-power/NOZZLE_HOT"},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			serves, fake := rig(t, "on", &fakeFleet{state: one.state})

			expectRefusal(t, post(t, serves, "printer-1", `{"on":false}`), http.StatusConflict, one.code)

			if calls := fake.recorded(); len(calls) != 0 {
				t.Errorf("Home Assistant was called: %+v", calls)
			}
		})
	}
}

func TestPowerOffAtTheEdgeOfTheGuardIsAllowed(t *testing.T) {
	t.Parallel()

	state := map[string]string{"/state": `"FINISHED"`, "/tools/0/nozzle_temp": `50`}
	serves, fake := rig(t, "on", &fakeFleet{state: state})

	if answered := post(t, serves, "printer-1", `{"on":false}`); answered.Status != http.StatusOK {
		t.Fatalf("answered %d: %s", answered.Status, answered.Body)
	}

	if len(fake.recorded()) != 1 {
		t.Error("Home Assistant was not called")
	}
}

func TestPowerOnIsAllowedWhilePrintingStateIsStaleAndWhenTheFleetCannotBeRead(t *testing.T) {
	t.Parallel()

	serves, _ := rig(t, "off", &fakeFleet{state: map[string]string{"/state": `"PRINTING"`}})
	if answered := post(t, serves, "printer-1", `{"on":true}`); answered.Status != http.StatusOK {
		t.Fatalf("answered %d: %s", answered.Status, answered.Body)
	}

	serves, _ = rig(t, "off", &fakeFleet{err: errors.New("host is busy")})
	if answered := post(t, serves, "printer-1", `{"on":true}`); answered.Status != http.StatusOK {
		t.Fatalf("answered %d: %s", answered.Status, answered.Body)
	}
}

func TestPowerOffIsRefusedWhenThePrinterStateCannotBeRead(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{err: errors.New("host is busy")})

	said := expectRefusal(t, post(t, serves, "printer-1", `{"on":false}`), http.StatusServiceUnavailable, "ha-power/STATE_UNREADABLE")
	if !said.Retryable {
		t.Error("an unreadable state is worth trying again")
	}

	if len(fake.recorded()) != 0 {
		t.Error("Home Assistant was called")
	}
}

func TestAPrinterThePersonMayNotSeeIsNotSwitched(t *testing.T) {
	t.Parallel()

	for _, err := range []error{deviceapi.ErrNoDevice, deviceapi.ErrDenied} {
		serves, fake := rig(t, "on", &fakeFleet{err: err})

		expectRefusal(t, post(t, serves, "printer-1", `{"on":true}`), http.StatusNotFound, "ha-power/NO_DEVICE")
		expectRefusal(t, post(t, serves, "printer-1", `{"on":false}`), http.StatusNotFound, "ha-power/NO_DEVICE")

		if len(fake.recorded()) != 0 {
			t.Error("Home Assistant was called")
		}
	}
}

func TestAHomeAssistantFailureIsARefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"error", http.StatusInternalServerError, "ha-power/HA_FAILED"},
		{"bad token", http.StatusUnauthorized, "ha-power/HA_AUTH"},
		{"no entity", http.StatusNotFound, "ha-power/HA_NO_ENTITY"},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()

			serves, fake := rig(t, "on", &fakeFleet{})
			fake.status = one.status

			expectRefusal(t, get(t, serves, "printer-1"), http.StatusBadGateway, one.code)
			expectRefusal(t, post(t, serves, "printer-1", `{"on":true}`), http.StatusBadGateway, one.code)
		})
	}
}

func TestAHomeAssistantThatIsNotThereIsUnreachable(t *testing.T) {
	t.Parallel()

	serves, fake := rig(t, "on", &fakeFleet{})
	fake.Close()

	said := expectRefusal(t, get(t, serves, "printer-1"), http.StatusBadGateway, "ha-power/HA_UNREACHABLE")
	if !said.Retryable {
		t.Error("an unreachable Home Assistant is worth trying again")
	}

	// The detail names no address.
	if strings.Contains(said.Detail, "127.0.0.1") {
		t.Errorf("the detail names the address: %s", said.Detail)
	}
}

func TestAHomeAssistantThatRedirectsIsNotFollowed(t *testing.T) {
	t.Parallel()

	elsewhere := newFakeHA(t, "on")
	redirecting := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusFound))
	t.Cleanup(redirecting.Close)

	held := newService(&fakeFleet{}, time.Second)
	serves := methods(held)
	configure(t, serves, map[string]any{
		"ha_url": redirecting.URL, "ha_token": token, "switches": map[string]string{"printer-1": "switch.mk4_plug"},
	})

	expectRefusal(t, get(t, serves, "printer-1"), http.StatusBadGateway, "ha-power/HA_FAILED")

	if calls := elsewhere.recorded(); len(calls) != 0 {
		t.Errorf("the redirect was followed: %+v", calls)
	}
}

func TestASlowHomeAssistantTimesOut(t *testing.T) {
	t.Parallel()

	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)

	held := newService(&fakeFleet{}, 50*time.Millisecond)
	serves := methods(held)
	configure(t, serves, map[string]any{
		"ha_url": slow.URL, "ha_token": token, "switches": map[string]string{"printer-1": "switch.mk4_plug"},
	})

	expectRefusal(t, get(t, serves, "printer-1"), http.StatusBadGateway, "ha-power/HA_UNREACHABLE")
}

func TestConfigureRefusesSettingsThatCannotWork(t *testing.T) {
	t.Parallel()

	good := func() map[string]any {
		return map[string]any{
			"ha_url": "http://homeassistant.local:8123", "ha_token": "t", "switches": map[string]string{"p": "switch.a_b"},
		}
	}

	if err := configure(t, methods(newService(&fakeFleet{}, time.Second)), good()); err != nil {
		t.Fatalf("good settings were refused: %v", err)
	}

	mistakes := map[string]func(map[string]any){
		"no scheme":      func(s map[string]any) { s["ha_url"] = "homeassistant.local:8123" },
		"ftp":            func(s map[string]any) { s["ha_url"] = "ftp://homeassistant.local" },
		"empty token":    func(s map[string]any) { s["ha_token"] = " " },
		"not a switch":   func(s map[string]any) { s["switches"] = map[string]string{"p": "light.kitchen"} },
		"a path":         func(s map[string]any) { s["switches"] = map[string]string{"p": "switch.a/../b"} },
		"empty printer":  func(s map[string]any) { s["switches"] = map[string]string{"": "switch.a"} },
		"a stray key":    func(s map[string]any) { s["extra"] = 1 },
		"switches array": func(s map[string]any) { s["switches"] = []string{"switch.a"} },
	}

	for name, mutate := range mistakes {
		settings := good()
		mutate(settings)

		if err := configure(t, methods(newService(&fakeFleet{}, time.Second)), settings); err == nil {
			t.Errorf("%s: the settings were accepted", name)
		}
	}
}

func TestConfigureCanArriveAgainWhileRequestsAreServed(t *testing.T) {
	t.Parallel()

	serves, _ := rig(t, "on", &fakeFleet{})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_ = configure(t, serves, map[string]any{
				"ha_url": "http://127.0.0.1:1", "ha_token": token, "switches": map[string]string{"printer-1": "switch.mk4_plug"},
			})
		}()

		go func() {
			defer wg.Done()

			get(t, serves, "printer-1")
		}()
	}

	wg.Wait()
}

func TestAPathTheModuleDoesNotServeReportsWhatItSaw(t *testing.T) {
	t.Parallel()

	serves, _ := rig(t, "on", &fakeFleet{})

	answered := callRoutes(t, serves, routesapi.Request{Method: http.MethodGet, Path: "/elsewhere", Principal: "user:alice"})
	if answered.Status != http.StatusNotFound || !strings.Contains(string(answered.Body), "/elsewhere") {
		t.Errorf("answered %d: %s", answered.Status, answered.Body)
	}
}
