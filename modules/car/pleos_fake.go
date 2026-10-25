package car

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

// The keys and vehicles a FakePleos serves: a Hyundai key with one car, and
// with ExpectKey_Kia a Kia key with another.
const (
	FakeClientID        = "hyundai-id"
	FakeClientSecret    = "hyundai-secret"
	FakeVIN             = "KMHTEST0000000001"
	FakeKiaClientID     = "kia-id"
	FakeKiaClientSecret = "kia-secret"
	FakeKiaVIN          = "KNATEST0000000002"
	// fakeUnknownVIN is listed by ExpectVehicle_Unknown but unknown to the
	// data endpoints.
	fakeUnknownVIN = "KMHGONE0000000003"
)

// FakeCar is a car a FakePleos reports, on top of the fixtures in testdata.
type FakeCar struct {
	Type       string
	IgnitionOn bool
	Charging   bool
	Latitude   float64
	Longitude  float64
	OdometerKm float64
	// NoData lists the sections that answer No Data, e.g. "locations".
	NoData []string
	// StatusUnreported makes the status section report nulls.
	StatusUnreported bool
	// Imperial reports the odometer in miles and the range in feet, with
	// invalid ignition and doors.
	Imperial bool
}

type fakeKey struct {
	secret string
	brand  string
	vins   []string
	// rejected makes the token endpoint refuse the key.
	rejected bool
}

// FakePleos serves the Pleos Vehicle Data API, for moduletest.HTTPClient.
// It checks the Brand header, tokens and which key a VIN belongs to as the
// API does.
type FakePleos struct {
	mu   sync.Mutex
	keys map[string]*fakeKey
	cars map[string]*FakeCar
	// tokens maps issued tokens to their client ID.
	tokens map[string]string
	issued int
	// tokenStatus, when set, is the token endpoint's error status.
	tokenStatus int
	// failing is the VINs whose data calls fail upstream.
	failing map[string]bool
	// expire is how many data calls reject their token as expired.
	expire   int
	requests []string
}

// FakeOption sets up a FakePleos.
type FakeOption func(testing.TB, *FakePleos)

// NewFake returns a FakePleos with the Hyundai key and its parked, locked EV.
func NewFake(tb testing.TB, opts ...FakeOption) *FakePleos {
	tb.Helper()
	f := &FakePleos{
		keys:    map[string]*fakeKey{FakeClientID: {secret: FakeClientSecret, brand: "hyundai", vins: []string{FakeVIN}}},
		cars:    map[string]*FakeCar{FakeVIN: {Type: "EV", Latitude: 52.374, Longitude: 4.8897, OdometerKm: 12345.6}},
		tokens:  map[string]string{},
		failing: map[string]bool{},
	}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectKey_Kia adds the Kia key with a parked combustion car.
func ExpectKey_Kia(_ testing.TB, f *FakePleos) {
	f.keys[FakeKiaClientID] = &fakeKey{secret: FakeKiaClientSecret, brand: "kia", vins: []string{FakeKiaVIN}}
	f.cars[FakeKiaVIN] = &FakeCar{Type: "GN", Latitude: 52.3, Longitude: 4.9, OdometerKm: 40000}
}

// ExpectVehicle_Combustion makes the Hyundai car a combustion car.
func ExpectVehicle_Combustion(_ testing.TB, f *FakePleos) { f.cars[FakeVIN].Type = "GN" }

// ExpectVehicle_Unknown makes the Hyundai key list a car the data endpoints
// do not know.
func ExpectVehicle_Unknown(_ testing.TB, f *FakePleos) {
	f.keys[FakeClientID].vins = append(f.keys[FakeClientID].vins, fakeUnknownVIN)
}

// ExpectLocation_NoData makes the Hyundai car report no position.
func ExpectLocation_NoData(_ testing.TB, f *FakePleos) {
	f.cars[FakeVIN].NoData = append(f.cars[FakeVIN].NoData, "locations")
}

// ExpectStatus_Unreported makes the status section report nulls, as a
// KONA Electric does for outside temperature, tires and sunroof.
func ExpectStatus_Unreported(_ testing.TB, f *FakePleos) {
	f.cars[FakeVIN].StatusUnreported = true
}

// Car changes the car with vin.
func (f *FakePleos) Car(vin string, fn func(*FakeCar)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.cars[vin])
}

// RejectKey revokes the key, as when it expires: its tokens stop working
// and the token endpoint refuses it.
func (f *FakePleos) RejectKey(clientID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[clientID].rejected = true
	maps.DeleteFunc(f.tokens, func(_, id string) bool { return id == clientID })
}

// TokenFails makes the token endpoint answer with status, e.g. 502 or 429,
// and every token expire, so the next call needs a new one.
func (f *FakePleos) TokenFails(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenStatus = status
	clear(f.tokens)
}

// FailVehicle makes the data calls for vin fail upstream, or work again.
func (f *FakePleos) FailVehicle(vin string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[vin] = fail
}

// ExpireTokens makes the next n data calls reject their token as expired.
func (f *FakePleos) ExpireTokens(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expire = n
}

// ExpectUnits_Imperial makes the Hyundai car report imperial units and
// invalid ignition and doors.
func ExpectUnits_Imperial(_ testing.TB, f *FakePleos) { f.cars[FakeVIN].Imperial = true }

// List makes the key list only vins.
func (f *FakePleos) List(clientID string, vins ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[clientID].vins = vins
}

// Reissue gives the key a new secret, as reissuing it in Playground does:
// its tokens stop working.
func (f *FakePleos) Reissue(clientID, secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[clientID].secret = secret
	maps.DeleteFunc(f.tokens, func(_, id string) bool { return id == clientID })
}

// Requests returns the requests served, as "brand METHOD path".
func (f *FakePleos) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Tokens returns how many access tokens were issued.
func (f *FakePleos) Tokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued
}

func fakeError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":%q,"message":"error %s","details":[],"timestamp":"2026-10-17T10:00:00+02:00"}}`, code, code)
}

func (f *FakePleos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	brand := r.Header.Get("Brand")
	f.requests = append(f.requests, brand+" "+r.Method+" "+r.URL.Path)
	if !slices.Contains(brands, brand) {
		fakeError(w, http.StatusBadRequest, "40001")
		return
	}
	if r.URL.Path == "/v1/auth/personal-token" {
		f.issue(w, r, brand)
		return
	}
	clientID, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok || f.expire > 0 {
		f.expire--
		fakeError(w, http.StatusBadRequest, codeTokenExpired)
		return
	}
	key := f.keys[clientID]
	if key.brand != brand {
		fakeError(w, http.StatusBadRequest, codeBadToken)
		return
	}
	if r.URL.Path == "/v1/vehicles/consent" {
		f.listing(w, key)
		return
	}
	rest, _ := strings.CutPrefix(r.URL.Path, "/v1/vehicles/")
	vin, section, _ := strings.Cut(rest, "/")
	c, ok := f.cars[vin]
	if !ok || !slices.Contains(key.vins, vin) {
		fakeError(w, http.StatusGone, codeUnknownVIN)
		return
	}
	if f.failing[vin] {
		fakeError(w, http.StatusBadGateway, "5001")
		return
	}
	if slices.Contains(c.NoData, section) {
		fakeError(w, http.StatusNotFound, codeNoData)
		return
	}
	name := section
	if section == "status" && c.StatusUnreported {
		name = "status_unreported"
	}
	body, err := fixtures.ReadFile("testdata/" + name + ".json")
	if err != nil {
		fakeError(w, http.StatusNotFound, "404")
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := doc["data"].(map[string]any)
	switch section {
	case "driving":
		data["ignition"].(map[string]any)["status"] = map[bool]string{true: "on", false: "off"}[c.IgnitionOn]
		data["odometer"].(map[string]any)["value"] = c.OdometerKm
		if c.Imperial {
			data["ignition"].(map[string]any)["status"] = "invalid"
			data["odometer"].(map[string]any)["unit"] = "mile"
		}
	case "powertrains":
		if c.Imperial {
			data["distanceToEmpties"] = []any{map[string]any{"type": "EV", "value": "4000", "unit": "feet"}, map[string]any{"type": "invalid", "value": "invalid", "unit": "invalid"}}
		}
	case "status":
		if c.Imperial {
			data["doors"] = []any{map[string]any{"left": "invalid", "right": "invalid"}}
		}
	case "locations":
		loc := data["location"].(map[string]any)
		loc["latitude"], loc["longitude"] = c.Latitude, c.Longitude
	case "batteries":
		data["charge"].(map[string]any)["charging"] = c.Charging
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (f *FakePleos) issue(w http.ResponseWriter, r *http.Request, brand string) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	key, ok := f.keys[body["clientId"]]
	switch {
	case f.tokenStatus != 0:
		fakeError(w, f.tokenStatus, strconv.Itoa(f.tokenStatus)+"01")
	case !ok || key.rejected || key.secret != body["clientSecret"] || key.brand != brand:
		fakeError(w, http.StatusNotFound, "40404")
	default:
		f.issued++
		token := fmt.Sprintf("token-%d", f.issued)
		f.tokens[token] = body["clientId"]
		fmt.Fprintf(w, `{"data":{"accessToken":%q},"meta":{"code":200,"message":"Success","success":true}}`, token)
	}
}

func (f *FakePleos) listing(w http.ResponseWriter, key *fakeKey) {
	vehicles := []map[string]string{}
	for _, vin := range key.vins {
		v := map[string]string{"vin": vin, "nickname": "", "name": "", "type": "EV", "sellname": ""}
		switch vin {
		case FakeVIN:
			v = map[string]string{"vin": vin, "nickname": "", "name": "SX2HV", "type": f.cars[vin].Type, "sellname": "KONA Electric"}
		case FakeKiaVIN:
			v = map[string]string{"vin": vin, "nickname": "Family", "name": "SP2", "type": f.cars[vin].Type, "sellname": "Seltos"}
		}
		vehicles = append(vehicles, v)
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"vehicles": vehicles}, "meta": map[string]any{"code": 200, "success": true}})
	w.Write(b)
}
