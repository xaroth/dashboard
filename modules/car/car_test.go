package car_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/car"
)

// testNow is half past midnight in Amsterdam, when it is still the day
// before in UTC.
var testNow = time.Date(2026, 10, 17, 0, 30, 0, 0, time.FixedZone("CEST", 2*3600))

// env is the module loaded against a fake, with its admin page.
type env struct {
	set  *module.Set
	mux  *http.ServeMux
	fake *car.FakePleos
	now  func() time.Time
}

// load loads the module against fake with the given config keys, at testNow.
func load(tb testing.TB, fake *car.FakePleos, keys string) env {
	tb.Helper()
	return open(tb, fake, tb.TempDir(), keys, func() time.Time { return testNow })
}

// open loads the module on the database in dir, as after a restart when
// it was loaded there before.
func open(tb testing.TB, fake *car.FakePleos, dir, keys string, now func() time.Time) env {
	tb.Helper()
	set := moduletest.Load(tb, moduletest.DB(tb, dir), "car: {api_url: http://pleos.test, "+keys+"}", module.Options{
		Registrations: []module.Registration{car.Module},
		HTTPClient:    moduletest.HTTPClient(fake),
		Now:           now,
	})
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	return env{set: set, mux: mux, fake: fake, now: now}
}

func (e env) serve(tb testing.TB, method, path string, form url.Values) *httptest.ResponseRecorder {
	tb.Helper()
	req := httptest.NewRequestWithContext(tb.Context(), method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// addKey saves a key on the admin page, issued today.
func (e env) addKey(tb testing.TB, brand, id, secret string) {
	tb.Helper()
	rec := e.serve(tb, http.MethodPost, "/car/keys", url.Values{
		"brand": {brand}, "client_id": {id}, "client_secret": {secret}, "issued": {e.now().Format(time.DateOnly)},
	})
	if rec.Code != http.StatusSeeOther {
		tb.Fatalf("saving key: got %d %s, want a redirect", rec.Code, rec.Body)
	}
}

func (e env) name(tb testing.TB, vin, name string) {
	tb.Helper()
	if rec := e.serve(tb, http.MethodPost, "/car/vehicles/name", url.Values{"vin": {vin}, "short_name": {name}}); rec.Code != http.StatusSeeOther {
		tb.Fatalf("naming %s: got %d %s, want a redirect", vin, rec.Code, rec.Body)
	}
}

func (e env) update(tb testing.TB) {
	tb.Helper()
	if err := e.set.Update(tb.Context(), "car"); err != nil {
		tb.Fatalf("Update: %v", err)
	}
}

func (e env) vehicle(tb testing.TB, id string) (*car.Vehicle, string) {
	tb.Helper()
	res := e.set.Resolve(tb.Context(), "car", "vehicle", map[string]any{"id": id})
	v, _ := res.Value.(*car.Vehicle)
	return v, res.Error
}

func (e env) vehicles(tb testing.TB) []car.Vehicle {
	tb.Helper()
	res := e.set.Resolve(tb.Context(), "car", "vehicles", nil)
	if res.Error != "" {
		tb.Fatalf("vehicles: %s", res.Error)
	}
	v, _ := res.Value.([]car.Vehicle)
	return v
}

func status(tb testing.TB, set *module.Set) module.Status {
	tb.Helper()
	st, err := set.Statuses(tb.Context())
	if err != nil || len(st) != 1 {
		tb.Fatalf("Statuses: %v, %v", st, err)
	}
	return st[0]
}

func ptr[T any](v T) *T { return &v }

// testExpiry is when a key issued on the day of testNow expires.
var testExpiry = time.Date(2027, 1, 15, 0, 0, 0, 0, testNow.Location())

func TestUpdate(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t), "")
	if v, _ := e.vehicle(t, ""); v != nil {
		t.Errorf("before any update: got %+v, want null", v)
	}
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.update(t)
	e.update(t)

	// The page checks the key; the first update gets its own token and
	// listing, which the second reuses.
	check := []string{"hyundai POST /v1/auth/personal-token", "hyundai GET /v1/vehicles/consent"}
	var data []string
	for _, s := range []string{"driving", "locations", "powertrains", "status", "batteries"} {
		data = append(data, "hyundai GET /v1/vehicles/"+car.FakeVIN+"/"+s)
	}
	want := append(append(append(check, check...), data...), data...)
	if diff := cmp.Diff(want, e.fake.Requests()); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}

	got, errMsg := e.vehicle(t, "")
	if got == nil || got.ActiveAt == nil {
		t.Fatalf("got %+v (%s), want the vehicle, active from its first poll", got, errMsg)
	}
	updated := time.Date(2026, 10, 17, 8, 5, 0, 0, time.UTC)
	wantVehicle := &car.Vehicle{
		VIN: car.FakeVIN, Brand: "hyundai", KeyExpiresAt: testExpiry, Model: "KONA Electric", ModelCode: "SX2HV", Type: "EV",
		UpdatedAt: &updated, ActiveAt: got.ActiveAt, IgnitionOn: ptr(false), OdometerKm: ptr(12345.6),
		Location:      &car.Position{Latitude: 52.2375, Longitude: 4.8264},
		StateOfCharge: ptr(80.5), Ranges: []car.Range{{Type: "EV", Km: 312}},
		Charge: &car.Charge{PluggedIn: true, Status: "notCharging", TargetAC: ptr(90), TargetDC: ptr(80), RemainingMinutes: ptr(0)},
		Locked: ptr(true), Open: []string{"trunk"}, ClimateOn: ptr(false), OutsideTemperature: ptr(10.0),
		Warnings: []string{"washFluid"},
	}
	if diff := cmp.Diff(wantVehicle, got); diff != "" {
		t.Errorf("vehicle (-want +got):\n%s", diff)
	}
}

func TestTwoKeys(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t, car.ExpectKey_Kia), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.addKey(t, "kia", car.FakeKiaClientID, car.FakeKiaClientSecret)
	e.update(t)
	e.name(t, car.FakeVIN, "Kona")

	// Named vehicles first; names are current without another update.
	var got [][3]string
	for _, v := range e.vehicles(t) {
		name := ""
		if v.ShortName != nil {
			name = *v.ShortName
		}
		got = append(got, [3]string{name, v.Brand, v.VIN})
	}
	want := [][3]string{{"kona", "hyundai", car.FakeVIN}, {"", "kia", car.FakeKiaVIN}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("vehicles (-want +got):\n%s", diff)
	}
	for _, r := range e.fake.Requests() {
		if strings.Contains(r, car.FakeKiaVIN) && !strings.HasPrefix(r, "kia ") {
			t.Errorf("got %s, want the kia brand", r)
		}
	}

	testCases := []struct {
		name    string
		id      string
		wantVIN string
		wantErr string
	}{
		{"first", "", car.FakeVIN, ""},
		{"short name", "kona", car.FakeVIN, ""},
		{"short name in capitals", "KONA", car.FakeVIN, ""},
		{"vin", car.FakeKiaVIN, car.FakeKiaVIN, ""},
		{"unknown", "nope", "", `no vehicle "nope"`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			v, errMsg := e.vehicle(t, testCase.id)
			switch {
			case testCase.wantErr != "" && !strings.Contains(errMsg, testCase.wantErr):
				t.Errorf("got error %q, want %q", errMsg, testCase.wantErr)
			case testCase.wantErr == "" && (v == nil || v.VIN != testCase.wantVIN):
				t.Errorf("got %+v (%s), want %s", v, errMsg, testCase.wantVIN)
			}
		})
	}
}

func TestKeyRejected(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t, car.ExpectKey_Kia), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.addKey(t, "kia", car.FakeKiaClientID, car.FakeKiaClientSecret)
	e.update(t)
	before, _ := e.vehicle(t, car.FakeKiaVIN)

	// One key rejected: the other is polled, the rejected one's vehicle
	// keeps its data, and the page shows why.
	e.fake.RejectKey(car.FakeKiaClientID)
	e.update(t)
	if after, _ := e.vehicle(t, car.FakeKiaVIN); after == nil || !after.ActiveAt.Equal(*before.ActiveAt) {
		t.Errorf("got %+v, want the kia vehicle as before", after)
	}
	if body := e.serve(t, http.MethodGet, "/car/", nil).Body.String(); !strings.Contains(body, "Last rejected") || !strings.Contains(body, "40404") {
		t.Errorf("got page without the rejection:\n%s", body)
	}

	// All keys rejected: the update stops for credentials.
	e.fake.RejectKey(car.FakeClientID)
	if err := e.set.Update(t.Context(), "car"); !errors.Is(err, module.ErrAuth) {
		t.Errorf("got %v, want ErrAuth", err)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		opts     []car.FakeOption
		after    func(*car.FakePleos)
		want     string // error code in the error; empty is success
		wantAuth bool
		tokens   int // including the page's
	}{
		{name: "token endpoint down", after: func(f *car.FakePleos) { f.TokenFails(http.StatusBadGateway) }, want: "50201", tokens: 1},
		{name: "token rate limited", after: func(f *car.FakePleos) { f.TokenFails(http.StatusTooManyRequests) }, want: "42901", tokens: 1},
		{name: "token expired", after: func(f *car.FakePleos) { f.ExpireTokens(1) }, tokens: 3},
		{name: "new token rejected", after: func(f *car.FakePleos) { f.ExpireTokens(1 << 30) }, want: "40003", wantAuth: true, tokens: 3},
		{name: "unknown vehicle", opts: []car.FakeOption{car.ExpectVehicle_Unknown}, want: "4101", tokens: 2},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := load(t, car.NewFake(t, testCase.opts...), "")
			e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
			if testCase.after != nil {
				testCase.after(e.fake)
			}
			err := e.set.Update(t.Context(), "car")
			switch {
			case testCase.want == "" && err != nil:
				t.Errorf("got %v, want success", err)
			case testCase.want != "" && (err == nil || !strings.Contains(err.Error(), testCase.want)):
				t.Errorf("got %v, want an error with code %s", err, testCase.want)
			case errors.Is(err, module.ErrAuth) != testCase.wantAuth:
				t.Errorf("got %v, want ErrAuth %v", err, testCase.wantAuth)
			}
			if got := e.fake.Tokens(); got != testCase.tokens {
				t.Errorf("got %d tokens issued, want %d", got, testCase.tokens)
			}
		})
	}
}

func TestUnknownVehicleRelisted(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t, car.ExpectVehicle_Unknown), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	if err := e.set.Update(t.Context(), "car"); err == nil {
		t.Fatal("got success, want the unknown vehicle to fail the update")
	}
	e.fake.List(car.FakeClientID, car.FakeVIN)
	e.update(t)
	var vins []string
	for _, v := range e.vehicles(t) {
		vins = append(vins, v.VIN)
	}
	if diff := cmp.Diff([]string{car.FakeVIN}, vins); diff != "" {
		t.Errorf("vehicles after relisting (-want +got):\n%s", diff)
	}
}

func TestKeyFailsTransiently(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t, car.ExpectKey_Kia), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.addKey(t, "kia", car.FakeKiaClientID, car.FakeKiaClientSecret)
	e.update(t)
	before, _ := e.vehicle(t, car.FakeKiaVIN)

	// An upstream error on one key: the other is polled, and the failed
	// key's vehicle keeps its data, without a rejection on the page.
	e.fake.FailVehicle(car.FakeKiaVIN, true)
	e.update(t)
	if after, _ := e.vehicle(t, car.FakeKiaVIN); after == nil || !after.ActiveAt.Equal(*before.ActiveAt) {
		t.Errorf("got %+v, want the kia vehicle as before", after)
	}
	if body := e.serve(t, http.MethodGet, "/car/", nil).Body.String(); strings.Contains(body, "Last rejected") {
		t.Errorf("got a rejection on the page for an upstream error:\n%s", body)
	}

	// On every key: the update fails, to back off, not for credentials.
	e.fake.FailVehicle(car.FakeVIN, true)
	if err := e.set.Update(t.Context(), "car"); err == nil || errors.Is(err, module.ErrAuth) || !strings.Contains(err.Error(), "5001") {
		t.Errorf("got %v, want the upstream error without ErrAuth", err)
	}
}

func TestKeyListsNoVehicles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := func() time.Time { return testNow }
	fake := car.NewFake(t)
	e := open(t, fake, dir, "", now)
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.update(t)
	// After a restart the key is listed again, and lists nothing.
	fake.List(car.FakeClientID)
	e = open(t, fake, dir, "", now)
	err := e.set.Update(t.Context(), "car")
	if !errors.Is(err, module.ErrAuth) || !strings.Contains(err.Error(), "no vehicles are shared") {
		t.Errorf("got %v, want ErrAuth for a key without vehicles", err)
	}
}

func TestKeyReissued(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.update(t)
	e.fake.Reissue(car.FakeClientID, "new-secret")
	if err := e.set.Update(t.Context(), "car"); !errors.Is(err, module.ErrAuth) {
		t.Fatalf("with the old secret: got %v, want ErrAuth", err)
	}
	// Saving the new secret replaces the key; the next update relists it.
	e.addKey(t, "hyundai", car.FakeClientID, "new-secret")
	e.update(t)
	if n := strings.Count(strings.Join(e.fake.Requests(), " "), "/consent"); n != 4 {
		t.Errorf("got %d listings, want 4: a check and an update for each secret", n)
	}
	if body := e.serve(t, http.MethodGet, "/car/", nil).Body.String(); strings.Contains(body, "Last rejected") {
		t.Errorf("got the old rejection on the page after saving the key again:\n%s", body)
	}
}

func TestExpiredKeyNotRetried(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t), "")
	rec := e.serve(t, http.MethodPost, "/car/keys", url.Values{
		"brand": {"hyundai"}, "client_id": {car.FakeClientID}, "client_secret": {car.FakeClientSecret},
		"issued": {testNow.AddDate(0, 0, -91).Format(time.DateOnly)},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("saving key: got %d %s, want 303", rec.Code, rec.Body)
	}
	e.fake.RejectKey(car.FakeClientID)
	var pages []string
	for range 3 {
		if err := e.set.Update(t.Context(), "car"); !errors.Is(err, module.ErrAuth) {
			t.Fatalf("got %v, want ErrAuth", err)
		}
		body := e.serve(t, http.MethodGet, "/car/", nil).Body.String()
		_, rejection, _ := strings.Cut(body, "Last rejected: ")
		rejection, _, _ = strings.Cut(rejection, "</p>")
		pages = append(pages, rejection)
	}
	// The rejection stays as recorded by the first attempt.
	if pages[0] == "" || pages[1] != pages[0] || pages[2] != pages[0] {
		t.Errorf("got rejections %q, want the first one three times", pages)
	}
	// The page's check, then one attempt; later updates ask nothing.
	want := []string{"hyundai POST /v1/auth/personal-token", "hyundai GET /v1/vehicles/consent", "hyundai POST /v1/auth/personal-token"}
	if diff := cmp.Diff(want, e.fake.Requests()); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
}

func TestImperialUnits(t *testing.T) {
	t.Parallel()
	e := load(t, car.NewFake(t, car.ExpectUnits_Imperial), "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.update(t)
	got, _ := e.vehicle(t, "")
	// As float64 at run time, not as exact constants.
	miles, feet := 12345.6, 4000.0
	if got.OdometerKm == nil || *got.OdometerKm != miles*1.609344 {
		t.Errorf("got odometer %v km, want 12345.6 miles in km", got.OdometerKm)
	}
	if diff := cmp.Diff([]car.Range{{Type: "EV", Km: feet * 0.0003048}}, got.Ranges); diff != "" {
		t.Errorf("ranges, without the invalid one (-want +got):\n%s", diff)
	}
	if got.IgnitionOn != nil || got.Locked != nil {
		t.Errorf("got ignition %v, locked %v, want both null for invalid values", got.IgnitionOn, got.Locked)
	}
}

func TestUpdateMissingData(t *testing.T) {
	t.Parallel()
	fake := car.NewFake(t, car.ExpectVehicle_Combustion, car.ExpectLocation_NoData, car.ExpectStatus_Unreported)
	e := load(t, fake, "")
	e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
	e.update(t)
	got, _ := e.vehicle(t, "")
	if got.Type != "GN" || got.Location != nil || got.Charge != nil || got.OutsideTemperature != nil {
		t.Errorf("got type %s, location %v, charge %v, outside temperature %v, want GN without location, charge or temperature",
			got.Type, got.Location, got.Charge, got.OutsideTemperature)
	}
	if got.FuelLevel == nil {
		t.Errorf("got no fuel level, want the combustion car's")
	}
	for _, r := range fake.Requests() {
		if strings.HasSuffix(r, "/batteries") {
			t.Errorf("got %s, want no battery request for a combustion car", r)
		}
	}
}

func TestAdminPage(t *testing.T) {
	t.Parallel()
	today := testNow.Format(time.DateOnly)
	key := func(change func(url.Values)) url.Values {
		v := url.Values{"brand": {"hyundai"}, "client_id": {car.FakeClientID}, "client_secret": {car.FakeClientSecret}, "issued": {today}}
		change(v)
		return v
	}
	testCases := []struct {
		name  string
		path  string
		form  url.Values
		setup func(*car.FakePleos)
		want  string
	}{
		{"unknown brand", "/car/keys", key(func(v url.Values) { v.Set("brand", "tesla") }), nil, "brand must be one of hyundai, kia, genesis"},
		{"no secret", "/car/keys", key(func(v url.Values) { v.Del("client_secret") }), nil, "client ID and client secret are required"},
		{"bad date", "/car/keys", key(func(v url.Values) { v.Set("issued", "17-10-2026") }), nil, "issue date must be a date"},
		{"future date", "/car/keys", key(func(v url.Values) { v.Set("issued", "2026-10-18") }), nil, "issue date is in the future"},
		{"key rejected", "/car/keys", key(func(v url.Values) { v.Set("client_secret", "wrong") }), nil, "checking the key with Pleos: listing vehicles: issuing access token: HTTP 404, code 40404"},
		{"wrong brand for key", "/car/keys", key(func(v url.Values) { v.Set("brand", "kia") }), nil, "code 40404"},
		{"key without vehicles", "/car/keys", key(func(url.Values) {}), func(f *car.FakePleos) { f.List(car.FakeClientID) }, "no vehicles are shared with this key"},
		{"bad short name", "/car/vehicles/name", url.Values{"vin": {car.FakeVIN}, "short_name": {"my car"}}, nil, "short name must be"},
		{"unknown vehicle", "/car/vehicles/name", url.Values{"vin": {"NOPE"}, "short_name": {"x"}}, nil, `no vehicle &#34;NOPE&#34;`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := load(t, car.NewFake(t), "")
			if testCase.setup != nil {
				testCase.setup(e.fake)
			}
			rec := e.serve(t, http.MethodPost, testCase.path, testCase.form)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), testCase.want) {
				t.Errorf("got %d, want 400 with %q:\n%s", rec.Code, testCase.want, rec.Body)
			}
			if body := e.serve(t, http.MethodGet, "/car/", nil).Body.String(); !strings.Contains(body, "No keys yet.") {
				t.Errorf("got a key saved, want none:\n%s", body)
			}
		})
	}

	t.Run("expiry", func(t *testing.T) {
		t.Parallel()
		testCases := []struct {
			name   string
			issued time.Time
			want   string
		}{
			{"fresh", testNow, "90 days left."},
			{"close", testNow.AddDate(0, 0, -85), "5 days left; reissue it"},
			{"expired", testNow.AddDate(0, 0, -91), "expired; reissue it"},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				t.Parallel()
				e := load(t, car.NewFake(t), "")
				form := key(func(v url.Values) { v.Set("issued", testCase.issued.Format(time.DateOnly)) })
				if rec := e.serve(t, http.MethodPost, "/car/keys", form); rec.Code != http.StatusSeeOther {
					t.Fatalf("saving key: got %d %s, want 303", rec.Code, rec.Body)
				}
				if body := e.serve(t, http.MethodGet, "/car/", nil).Body.String(); !strings.Contains(body, testCase.want) {
					t.Errorf("got page without %q:\n%s", testCase.want, body)
				}
			})
		}
	})

	t.Run("names and delete", func(t *testing.T) {
		t.Parallel()
		e := load(t, car.NewFake(t, car.ExpectKey_Kia), "")
		e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
		e.addKey(t, "kia", car.FakeKiaClientID, car.FakeKiaClientSecret)
		e.name(t, car.FakeVIN, "kona")
		rec := e.serve(t, http.MethodPost, "/car/vehicles/name", url.Values{"vin": {car.FakeKiaVIN}, "short_name": {"kona"}})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "is taken") {
			t.Errorf("duplicate name: got %d, want 400 taken:\n%s", rec.Code, rec.Body)
		}
		e.name(t, car.FakeVIN, "") // clears it
		e.name(t, car.FakeKiaVIN, "kona")

		if rec := e.serve(t, http.MethodPost, "/car/keys/delete", url.Values{"client_id": {car.FakeKiaClientID}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("delete: got %d %s, want 303", rec.Code, rec.Body)
		}
		e.update(t)
		var vins []string
		for _, v := range e.vehicles(t) {
			vins = append(vins, v.VIN)
			if v.ShortName != nil {
				t.Errorf("got short name %s on %s, want it cleared", *v.ShortName, v.VIN)
			}
		}
		if diff := cmp.Diff([]string{car.FakeVIN}, vins); diff != "" {
			t.Errorf("vehicles after deleting the kia key (-want +got):\n%s", diff)
		}
	})
}

func TestConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		keys string
		want module.Phase
	}{
		{"defaults", "", module.PhaseWaiting},
		{"zero active interval", "active_interval: 0s", module.PhaseInitFail},
		{"max below active", "active_interval: 1h, max_interval: 30m", module.PhaseInitFail},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := status(t, load(t, car.NewFake(t), testCase.keys).set).Phase; got != testCase.want {
				t.Errorf("got phase %s, want %s", got, testCase.want)
			}
		})
	}
}

// run starts the schedule in a synctest bubble and stops it at the end.
func run(t *testing.T, fake *car.FakePleos, check func(t *testing.T, e env)) {
	t.Helper()
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		e := open(t, fake, t.TempDir(), "", time.Now)
		done := make(chan struct{})
		go func() {
			e.set.Run(ctx)
			close(done)
		}()
		check(t, e)
		cancel()
		<-done
	})
}

func TestDisabledWithoutKey(t *testing.T) {
	run(t, car.NewFake(t), func(t *testing.T, e env) {
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := status(t, e.set).Phase; got != module.PhaseDisabled {
			t.Errorf("got phase %s, want %s", got, module.PhaseDisabled)
		}
	})
}

// nextPoll waits for the next scheduled update and returns the delay it set.
func nextPoll(t *testing.T, set *module.Set) time.Duration {
	t.Helper()
	before := status(t, set).FetchedAt
	for {
		// Jump to the scheduled run, or past the start jitter.
		step := time.Second
		if st := status(t, set); st.NextRun.After(time.Now()) {
			step = st.NextRun.Sub(time.Now())
		}
		time.Sleep(step)
		synctest.Wait()
		st := status(t, set)
		if st.Error != "" {
			t.Fatalf("update failed: %s", st.Error)
		}
		if st.FetchedAt != nil && (before == nil || st.FetchedAt.After(*before)) {
			return st.NextRun.Sub(*st.FetchedAt)
		}
	}
}

// TestPolling runs the schedule: 15 minutes while the car is in use, a
// quarter of the idle time while it is parked, at most 12 hours.
func TestPolling(t *testing.T) {
	fake := car.NewFake(t)
	run(t, fake, func(t *testing.T, e env) {
		e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
		set := e.set
		next := func() time.Duration { t.Helper(); return nextPoll(t, set) }
		change := func(fn func(*car.FakeCar)) { fake.Car(car.FakeVIN, fn) }

		if got := next(); got != 15*time.Minute {
			t.Errorf("first poll: got next in %v, want 15m", got)
		}
		// GPS jitter of about 50 m is not movement.
		change(func(c *car.FakeCar) { c.Latitude += 0.00045 })
		var idle []time.Duration
		for range 6 {
			idle = append(idle, next())
		}
		// Parked 15m, 30m, 45m, 1h, 1h15m, 1h33m45s.
		want := []time.Duration{15 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute,
			18*time.Minute + 45*time.Second, 23*time.Minute + 26*time.Second + 250*time.Millisecond}
		if diff := cmp.Diff(want, idle); diff != "" {
			t.Errorf("idle ramp (-want +got):\n%s", diff)
		}

		// After 4 hours parked, each signal brings it back to 15 minutes.
		testCases := []struct {
			name      string
			set, undo func(*car.FakeCar)
		}{
			{"moved a kilometer", func(c *car.FakeCar) { c.Latitude += 0.009 }, nil},
			{"ignition on", func(c *car.FakeCar) { c.IgnitionOn = true }, func(c *car.FakeCar) { c.IgnitionOn = false }},
			{"charging", func(c *car.FakeCar) { c.Charging = true }, func(c *car.FakeCar) { c.Charging = false }},
			// For cars without a position.
			{"odometer", func(c *car.FakeCar) { c.NoData = []string{"locations"}; c.OdometerKm += 3 }, nil},
		}
		for _, testCase := range testCases {
			time.Sleep(4 * time.Hour)
			synctest.Wait()
			if st := status(t, set); st.NextRun.Sub(*st.FetchedAt) <= 15*time.Minute {
				t.Fatalf("before %s: got next poll in %v, want the parked ramp", testCase.name, st.NextRun.Sub(*st.FetchedAt))
			}
			change(testCase.set)
			if got := next(); got != 15*time.Minute {
				t.Errorf("%s: got next poll in %v, want 15m", testCase.name, got)
			}
			if testCase.undo != nil {
				change(testCase.undo)
			}
		}

		// Parked for days: capped at 12 hours.
		time.Sleep(72 * time.Hour)
		synctest.Wait()
		if got := next(); got != 12*time.Hour {
			t.Errorf("parked for days: got next poll in %v, want 12h", got)
		}
	})
}

// A failing key does not hide that its car is in use while another is
// parked.
func TestPollingKeyFails(t *testing.T) {
	fake := car.NewFake(t, car.ExpectKey_Kia)
	run(t, fake, func(t *testing.T, e env) {
		e.addKey(t, "hyundai", car.FakeClientID, car.FakeClientSecret)
		e.addKey(t, "kia", car.FakeKiaClientID, car.FakeKiaClientSecret)
		nextPoll(t, e.set)
		time.Sleep(4 * time.Hour)
		synctest.Wait()
		fake.Car(car.FakeKiaVIN, func(c *car.FakeCar) { c.Latitude += 0.009 })
		if got := nextPoll(t, e.set); got != 15*time.Minute {
			t.Fatalf("kia driven: got next poll in %v, want 15m", got)
		}
		fake.FailVehicle(car.FakeKiaVIN, true)
		if got := nextPoll(t, e.set); got != 15*time.Minute {
			t.Errorf("kia key failing: got next poll in %v, want 15m", got)
		}
	})
}
