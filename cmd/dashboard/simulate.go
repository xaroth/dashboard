package main

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/render"
)

//go:embed simulate.html
var simulateHTML string

var simulateTmpl = template.Must(template.New("simulate").Parse(simulateHTML))

func (h *adminHandler) status(name string) (device.Status, bool) {
	statuses := h.Devices.Statuses()
	i := slices.IndexFunc(statuses, func(s device.Status) bool { return s.Name == name })
	if i < 0 {
		return device.Status{}, false
	}
	return statuses[i], true
}

func (h *adminHandler) simulatePage(w http.ResponseWriter, r *http.Request) {
	st, ok := h.status(r.PathValue("device"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	models := slices.Sorted(maps.Keys(render.Panels))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := simulateTmpl.Execute(w, map[string]any{"Device": st, "Models": models}); err != nil {
		h.Log.Error("rendering simulator", "err", err)
	}
}

// exchange is one simulated request to the device API and its response.
type exchange struct {
	Request  map[string]string `json:"request"`
	Status   int               `json:"status"`
	Response json.RawMessage   `json:"response"`
}

// simulateWake plays a device wake against the real device API: setup when
// no key is known yet, then display with the headers the firmware sends. The
// simulator header gives it separate state, so the real device is untouched.
func (h *adminHandler) simulateWake(w http.ResponseWriter, r *http.Request) {
	st, ok := h.status(r.PathValue("device"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	model := q.Get("model")
	panel, ok := render.Panels[model]
	if !ok {
		http.Error(w, "unknown model", http.StatusBadRequest)
		return
	}
	source := q.Get("source")
	if source != "timer" && source != "button" {
		http.Error(w, "source must be timer or button", http.StatusBadRequest)
		return
	}

	var log []exchange
	h.simMu.Lock()
	key := h.simKeys[st.Name]
	h.simMu.Unlock()
	if key == "" {
		ex := h.call(r, "/api/setup", map[string]string{"ID": st.MAC, "Model": model, "FW-Version": "simulator"})
		log = append(log, ex)
		var setup struct {
			APIKey string `json:"api_key"`
		}
		json.Unmarshal(ex.Response, &setup)
		key = setup.APIKey
		h.simMu.Lock()
		if h.simKeys == nil {
			h.simKeys = map[string]string{}
		}
		h.simKeys[st.Name] = key
		h.simMu.Unlock()
	}
	log = append(log, h.call(r, "/api/display", map[string]string{
		"ID":              st.MAC,
		"Access-Token":    key,
		"Model":           model,
		"Width":           strconv.Itoa(panel.Width),
		"Height":          strconv.Itoa(panel.Height),
		"Battery-Voltage": "4.05",
		"RSSI":            "-61",
		"FW-Version":      "simulator",
		"Update-Source":   source,
	}))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(log)
}

func (h *adminHandler) call(r *http.Request, path string, headers map[string]string) exchange {
	req := httptest.NewRequestWithContext(r.Context(), http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set(device.SimulatorHeader, "1")
	rec := httptest.NewRecorder()
	h.DeviceAPI.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	if !json.Valid(body) {
		body, _ = json.Marshal(rec.Body.String())
	}
	return exchange{Request: headers, Status: rec.Code, Response: body}
}
