package device

import (
	"cmp"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/service/render"
)

const (
	// Until the screen schedule lands, devices wake on a fixed interval.
	mainRefresh   = 900
	detailRefresh = 300
	maxLogBody    = 64 << 10
)

// Register adds the device protocol routes to mux.
func (d *Devices) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup", d.setup)
	mux.HandleFunc("GET /api/display", d.display)
	mux.HandleFunc("POST /api/log", d.deviceLog)
	mux.HandleFunc("GET /images/{file}", d.image)
}

func (d *Devices) setup(w http.ResponseWriter, r *http.Request) {
	mac, ok := parseMAC(r.Header.Get("ID"))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"status": 404, "message": "missing or invalid ID header"})
		return
	}
	d.mu.Lock()
	s, ok := d.byMAC[mac]
	if !ok {
		d.pending[mac] = d.now()
		d.mu.Unlock()
		d.log.Info("setup from unknown device", "mac", mac)
		// HTTP 200 with status 404: the firmware shows "MAC not registered" and retries.
		writeJSON(w, http.StatusOK, map[string]any{"status": 404, "message": "MAC not registered"})
		return
	}
	if s.apiKey == "" {
		s.apiKey = newAPIKey()
	}
	key := s.apiKey
	d.mu.Unlock()
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
	s.model = cmp.Or(r.Header.Get("Model"), s.model)
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

	refresh := mainRefresh
	if button {
		refresh = detailRefresh
	}
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
	} else {
		d.store.Put(filename, img.PNG)
	}
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

// authenticate finds the device by its ID header and checks Access-Token.
// After a restart keys are gone; the first token a known device presents is
// adopted, so devices keep working without running setup again.
func (d *Devices) authenticate(w http.ResponseWriter, r *http.Request) (*state, bool) {
	mac, _ := parseMAC(r.Header.Get("ID"))
	token := r.Header.Get("Access-Token")
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.byMAC[mac]
	if !ok || token == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"status": 404, "message": "device not registered"})
		return nil, false
	}
	if s.apiKey == "" {
		s.apiKey = token
	}
	if token != s.apiKey {
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

func parseMAC(s string) (string, bool) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", false
	}
	return strings.ToUpper(hw.String()), true
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
