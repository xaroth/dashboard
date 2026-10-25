package car

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

// brands are the manufacturers Pleos serves; a key is for one of them, sent
// as the Brand header on every call.
var brands = []string{"hyundai", "kia", "genesis"}

// Pleos error codes the module acts on.
const (
	codeTokenExpired = "40003"
	codeTokenInvalid = "40004"
	codeBadToken     = "4011"
	codeNoData       = "4045"
	codeNoConsent    = "5006"
	codeUnknownVIN   = "4101"
)

// apiError is a Pleos error response.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d, code %s: %s", e.status, e.code, e.message)
}

func (e *apiError) is(codes ...string) bool { return slices.Contains(codes, e.code) }

func asAPIError(err error) (*apiError, bool) { return errors.AsType[*apiError](err) }

// pleos calls the Vehicle Data API with one My Vehicle API key. Access tokens
// have no refresh token and an undocumented lifetime: one is issued on first
// use and again when the API rejects it.
type pleos struct {
	url          string
	brand        string
	clientID     string
	clientSecret string
	token        string
}

// rejections are the token endpoint's statuses for a key that does not work
// (unknown, revoked, expired, bad parameters); others, like 429, are worth
// retrying.
var rejections = []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}

// issueToken exchanges the key for an access token. A rejected key is
// module.ErrAuth: retrying will not help.
func (p *pleos) issueToken(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"clientId": p.clientID, "clientSecret": p.clientSecret})
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	err := p.send(ctx, http.MethodPost, "/v1/auth/personal-token", body, "", &out)
	if e, ok := asAPIError(err); ok && slices.Contains(rejections, e.status) {
		return fmt.Errorf("issuing access token: %w: %w", err, module.ErrAuth)
	}
	if err != nil {
		return fmt.Errorf("issuing access token: %w", err)
	}
	if out.AccessToken == "" {
		return errors.New("issuing access token: response has no access token")
	}
	p.token = out.AccessToken
	return nil
}

// get fetches path into out, issuing a token first if needed and once more
// if the API rejects the current one.
func (p *pleos) get(ctx context.Context, path string, out any) error {
	if p.token == "" {
		if err := p.issueToken(ctx); err != nil {
			return err
		}
	}
	err := p.send(ctx, http.MethodGet, path, nil, p.token, out)
	if e, ok := asAPIError(err); !ok || !(e.status == http.StatusUnauthorized || e.is(codeTokenExpired, codeTokenInvalid, codeBadToken)) {
		return err
	}
	if err := p.issueToken(ctx); err != nil {
		return err
	}
	err = p.send(ctx, http.MethodGet, path, nil, p.token, out)
	if e, ok := asAPIError(err); ok && (e.status == http.StatusUnauthorized || e.is(codeTokenExpired, codeTokenInvalid, codeBadToken)) {
		return fmt.Errorf("%w: %w", err, module.ErrAuth)
	}
	return err
}

func (p *pleos) send(ctx context.Context, method, path string, body []byte, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, p.url+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Brand", p.brand)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := module.HTTPClient(ctx).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// Errors come with HTTP 400 as often as with their own status;
		// code tells them apart.
		var e struct {
			Error struct {
				Code    json.RawMessage `json:"code"`
				Message string          `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		// code is documented as a string but sometimes comes as a number.
		code := strings.Trim(string(e.Error.Code), `"`)
		return &apiError{status: resp.StatusCode, code: code, message: cmp.Or(e.Error.Message, resp.Status)}
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decoding response data: %w", err)
	}
	return nil
}

type listedVehicle struct {
	VIN      string `json:"vin"`
	Nickname string `json:"nickname"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Sellname string `json:"sellname"`
}

func (p *pleos) vehicles(ctx context.Context) ([]listedVehicle, error) {
	var out struct {
		Vehicles []listedVehicle `json:"vehicles"`
	}
	if err := p.get(ctx, "/v1/vehicles/consent", &out); err != nil {
		return nil, fmt.Errorf("listing vehicles: %w", err)
	}
	return out.Vehicles, nil
}

// apiTime is a timestamp the car sent. Empty and null mean unknown.
type apiTime struct{ time.Time }

func (t *apiTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil || s == "" {
		return nil
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("timestamp %q: %w", s, err)
	}
	t.Time = v
	return nil
}

// valueUnit is a measurement; Value is null when the car does not report it.
type valueUnit struct {
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}

type driving struct {
	Ignition *struct {
		Status string `json:"status"`
	} `json:"ignition"`
	Odometer  *valueUnit `json:"odometer"`
	Timestamp apiTime    `json:"timestamp"`
}

type location struct {
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	Timestamp apiTime `json:"timestamp"`
}

type batteries struct {
	Charge *struct {
		Plugin              string   `json:"plugin"`
		Status              string   `json:"status"`
		Charging            *bool    `json:"charging"`
		StateOfCharge       *float64 `json:"stateOfCharge"`
		TargetStateOfCharge *struct {
			Standard *int `json:"standard"`
			Quick    *int `json:"quick"`
		} `json:"targetStateOfCharge"`
		RemainTime *int `json:"remainTime"`
	} `json:"charge"`
	Timestamp apiTime `json:"timestamp"`
}

type powertrains struct {
	Engine *struct {
		FuelLevel *float64 `json:"fuelLevel"`
	} `json:"engine"`
	Batteries []struct {
		Type        string   `json:"type"`
		RemainRatio *float64 `json:"remainRatio"`
	} `json:"batteries"`
	DistanceToEmpties []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
		Unit  string `json:"unit"`
	} `json:"distanceToEmpties"`
	Timestamp apiTime `json:"timestamp"`
}

type sides struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type vehicleStatus struct {
	Doors          []sides  `json:"doors"`
	Hood           string   `json:"hood"`
	Trunk          string   `json:"trunk"`
	Sunroof        string   `json:"sunroof"`
	Warnings       []string `json:"warnings"`
	ClimateControl *struct {
		Status string `json:"status"`
	} `json:"climateControl"`
	OutsideTemperature *valueUnit `json:"outsideTemperature"`
	Timestamp          apiTime    `json:"timestamp"`
}

// reading is one poll of a vehicle. A section is nil when the API has no
// data for it: not every model reports every field.
type reading struct {
	driving     *driving
	location    *location
	batteries   *batteries
	powertrains *powertrains
	status      *vehicleStatus
}

// read polls vin. Battery data is only asked of vehicles that charge.
func (p *pleos) read(ctx context.Context, vin, vehicleType string) (reading, error) {
	var r reading
	base := "/v1/vehicles/" + vin
	if err := fetch(ctx, p, base+"/driving", &r.driving); err != nil {
		return r, err
	}
	if err := fetch(ctx, p, base+"/locations", &r.location); err != nil {
		return r, err
	}
	if err := fetch(ctx, p, base+"/powertrains", &r.powertrains); err != nil {
		return r, err
	}
	if err := fetch(ctx, p, base+"/status", &r.status); err != nil {
		return r, err
	}
	if slices.Contains([]string{"EV", "PE", "UNKNOWN"}, vehicleType) {
		if err := fetch(ctx, p, base+"/batteries", &r.batteries); err != nil {
			return r, err
		}
	}
	return r, nil
}

// fetch gets path into *dst, leaving it nil when the API has no data.
func fetch[T any](ctx context.Context, p *pleos, path string, dst **T) error {
	v := new(T)
	err := p.get(ctx, path, v)
	if e, ok := asAPIError(err); ok && e.is(codeNoData) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	*dst = v
	return nil
}
