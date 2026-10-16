package device

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/render"
)

const maxLogBody = 64 << 10

// Register adds the device protocol routes to mux.
func (d *Devices) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup", d.setup)
	mux.HandleFunc("GET /api/display", d.display)
	mux.HandleFunc("POST /api/log", d.deviceLog)
	mux.HandleFunc("GET /images/{file}", d.image)
}

func (d *Devices) setup(w http.ResponseWriter, r *http.Request) {
	mac, err := NormalizeMAC(r.Header.Get("ID"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": 404, "message": "missing or invalid ID header"})
		return
	}
	d.mu.Lock()
	s, ok := d.byMAC[mac]
	if !ok {
		if _, seen := d.pending[mac]; seen || len(d.pending) < maxPending {
			d.pending[mac] = d.now()
		}
		d.mu.Unlock()
		d.log.Info("setup from unknown device", "mac", mac)
		// HTTP 200 with status 404: the firmware shows "MAC not registered" and retries.
		writeJSON(w, http.StatusOK, map[string]any{"status": 404, "message": "MAC not registered"})
		return
	}
	d.mu.Unlock()
	key, err := d.apiKey(r.Context(), mac)
	if err != nil {
		d.log.Error("reading api key", "device", s.cfg.Name, "err", err)
		// A plain HTTP error: the firmware retries setup later.
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	d.log.Info("device setup", "device", s.cfg.Name, "model", r.Header.Get("Model"))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      200,
		"api_key":     key,
		"friendly_id": friendlyID(mac),
		"message":     "Registered as " + s.cfg.Name,
	})
}

func (d *Devices) display(w http.ResponseWriter, r *http.Request) {
	s, ok := d.authenticate(w, r)
	if !ok {
		return
	}
	source := r.Header.Get("Update-Source")
	button := source == "button" || source == "EXT0"

	d.mu.Lock()
	if model := r.Header.Get("Model"); render.Panels[model] != (render.Panel{}) {
		s.model = model
	}
	s.wake = wakeFrom(r, d.now())
	if button {
		s.page = (s.page + 1) % len(s.cfg.Pages)
	} else {
		s.page = 0
	}
	page := s.cfg.Pages[s.page]
	target := s.target()
	prev := s.filename
	d.mu.Unlock()

	refresh := d.schedule.refresh(d.now(), button)
	img, err := d.pipeline.Render(r.Context(), target, page)
	if err != nil {
		d.log.Error("rendering for device", "device", target.Name, "page", page, "err", err)
		if prev == "" {
			// Never JSON status 500: the firmware treats it as a reset. A plain
			// HTTP error makes it retry later.
			http.Error(w, "no image available", http.StatusServiceUnavailable)
			return
		}
		d.writeDisplay(w, prev, refresh)
		return
	}

	filename := img.Filename
	// Same pixels as last time: keep the name so the device skips the download.
	if prev != "" && imageHash(prev) == imageHash(filename) {
		filename = prev
	}
	// Also on reuse: another device's images may have evicted it, and a device
	// with an empty cache downloads it again.
	d.store.Put(filename, img.PNG)
	d.mu.Lock()
	s.filename = filename
	d.mu.Unlock()
	d.log.Info("display", "device", target.Name, "page", page, "source", source, "filename", filename)
	d.writeDisplay(w, filename, refresh)
}

func (d *Devices) writeDisplay(w http.ResponseWriter, filename string, refresh int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           0,
		"image_url":        d.publicURL + "/images/" + filename + ".png",
		"filename":         filename,
		"refresh_rate":     refresh,
		"reset_firmware":   false,
		"update_firmware":  false,
		"firmware_url":     nil,
		"special_function": "sleep",
	})
}

func (d *Devices) deviceLog(w http.ResponseWriter, r *http.Request) {
	s, ok := d.authenticate(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLogBody))
	if err != nil {
		http.Error(w, "reading body", http.StatusBadRequest)
		return
	}
	d.log.Info("device log", "device", s.cfg.Name, "body", string(body))
	w.WriteHeader(http.StatusNoContent)
}

func (d *Devices) image(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".png")
	if !ok {
		http.NotFound(w, r)
		return
	}
	png, ok := d.store.Get(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.Write(png)
}

// apiKey returns mac's stored key, creating one on its first setup.
func (d *Devices) apiKey(ctx context.Context, mac string) (string, error) {
	key, err := d.queries.GetAPIKey(ctx, mac)
	if !errors.Is(err, sql.ErrNoRows) {
		return key, err
	}
	if err := d.queries.CreateAPIKey(ctx, CreateAPIKeyParams{Mac: mac, ApiKey: newAPIKey()}); err != nil {
		return "", err
	}
	// Read back: a concurrent setup may have stored its key first.
	return d.queries.GetAPIKey(ctx, mac)
}

// authenticate finds the device by its ID header and checks Access-Token
// against the key stored at setup. A device without a stored key gets 401
// until it runs setup again.
func (d *Devices) authenticate(w http.ResponseWriter, r *http.Request) (*state, bool) {
	mac, _ := NormalizeMAC(r.Header.Get("ID"))
	token := r.Header.Get("Access-Token")
	d.mu.Lock()
	s, ok := d.byMAC[mac]
	d.mu.Unlock()
	if !ok || token == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"status": 404, "message": "device not registered"})
		return nil, false
	}
	key, err := d.queries.GetAPIKey(r.Context(), mac)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		d.log.Error("reading api key", "device", s.cfg.Name, "err", err)
		// Never JSON status 500, which resets the firmware.
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	if key == "" || subtle.ConstantTimeCompare([]byte(token), []byte(key)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"status": 401, "message": "invalid Access-Token"})
		return nil, false
	}
	return s, true
}

func wakeFrom(r *http.Request, now time.Time) *render.Wake {
	wake := &render.Wake{
		Source:   r.Header.Get("Update-Source"),
		Firmware: r.Header.Get("FW-Version"),
		At:       now,
	}
	if v, err := strconv.ParseFloat(r.Header.Get("Battery-Voltage"), 64); err == nil {
		wake.Battery = &v
	}
	if v, err := strconv.Atoi(r.Header.Get("RSSI")); err == nil {
		wake.RSSI = &v
	}
	return wake
}

// NormalizeMAC returns mac in upper case with colons, the form the firmware sends.
func NormalizeMAC(mac string) (string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid mac %q", mac)
	}
	return strings.ToUpper(hw.String()), nil
}

// friendlyID is the last three bytes of the MAC, shown on the device's setup screen.
func friendlyID(mac string) string {
	return strings.ReplaceAll(mac[9:], ":", "")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// imageHash is the content part of a filename, before the timestamp.
func imageHash(filename string) string {
	hash, _, _ := strings.Cut(filename, "-")
	return hash
}
