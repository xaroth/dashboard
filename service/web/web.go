// Package web serves the admin UI: status and previews of what devices get.
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/xaroth/dashboard/service/device"
	"github.com/xaroth/dashboard/service/modules"
	"github.com/xaroth/dashboard/service/render"
)

type Pipeline interface {
	Pages(ctx context.Context) ([]string, error)
	Props(ctx context.Context, t render.Target, page string) (render.Props, error)
	Render(ctx context.Context, t render.Target, page string) (render.Image, error)
}

type Devices interface {
	Statuses() []device.Status
	Pending() []device.Pending
	// Target is how a device is rendered: its last reported model and wake.
	Target(name string) (render.Target, bool)
}

type Modules interface {
	Statuses(ctx context.Context) ([]modules.Status, error)
	Update(ctx context.Context, name string) error
}

type Handler struct {
	Pipeline Pipeline
	Devices  Devices
	Modules  Modules
	// DeviceAPI serves the device protocol; the simulator calls it in-process.
	DeviceAPI http.Handler
	Log       *slog.Logger

	simMu   sync.Mutex
	simKeys map[string]string // device name -> api_key from setup
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("POST /modules/{module}/update", h.updateModule)
	mux.HandleFunc("GET /simulate/{device}", h.simulatePage)
	mux.HandleFunc("POST /simulate/{device}/wake", h.simulateWake)
	mux.Handle("GET /images/{file}", h.DeviceAPI)
	mux.HandleFunc("GET /preview/page/{file}", h.previewPage)
	mux.HandleFunc("GET /preview/{device}/{file}", h.previewDevice)
	mux.HandleFunc("GET /preview/{device}/{page}/props.json", h.previewProps)
}

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

type panelView struct {
	Model    string
	Rotation int
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	models := make([]string, 0, len(render.Panels))
	for m := range render.Panels {
		models = append(models, m)
	}
	slices.Sort(models)
	var panels []panelView
	for _, m := range models {
		for _, rot := range []int{0, 90} {
			panels = append(panels, panelView{m, rot})
		}
	}
	pages, err := h.Pipeline.Pages(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	mods, err := h.Modules.Statuses(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = indexTmpl.Execute(w, map[string]any{
		"Devices": h.Devices.Statuses(),
		"Pending": h.Devices.Pending(),
		"Modules": mods,
		"Pages":   pages,
		"Panels":  panels,
	})
	if err != nil {
		h.Log.Error("rendering index", "err", err)
	}
}

func (h *Handler) previewPage(w http.ResponseWriter, r *http.Request) {
	page, ok := strings.CutSuffix(r.PathValue("file"), ".png")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	t := render.Target{Name: "preview", Model: q.Get("model"), Rotation: 0}
	if t.Model == "" {
		t.Model = device.DefaultModel
	}
	if s := q.Get("rotation"); s != "" {
		rot, err := strconv.Atoi(s)
		if err != nil || (rot != 0 && rot != 90 && rot != 180 && rot != 270) {
			http.Error(w, "rotation must be 0, 90, 180 or 270", http.StatusBadRequest)
			return
		}
		t.Rotation = rot
	}
	h.servePNG(w, r, t, page)
}

func (h *Handler) previewDevice(w http.ResponseWriter, r *http.Request) {
	page, ok := strings.CutSuffix(r.PathValue("file"), ".png")
	if !ok {
		http.NotFound(w, r)
		return
	}
	t, ok := h.target(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.servePNG(w, r, t, page)
}

func (h *Handler) previewProps(w http.ResponseWriter, r *http.Request) {
	t, ok := h.target(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	props, err := h.Pipeline.Props(r.Context(), t, r.PathValue("page"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(props)
}

// target resolves the device in the path; ?model= overrides its model.
func (h *Handler) target(r *http.Request) (render.Target, bool) {
	t, ok := h.Devices.Target(r.PathValue("device"))
	if !ok {
		return t, false
	}
	if m := r.URL.Query().Get("model"); m != "" {
		t.Model = m
	}
	return t, true
}

func (h *Handler) servePNG(w http.ResponseWriter, r *http.Request, t render.Target, page string) {
	img, err := h.Pipeline.Render(r.Context(), t, page)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(img.PNG)
}

// fail maps pipeline errors to a status: the caller's mistakes are 4xx, the
// renderer's are 500 and logged.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, render.ErrUnknownPage):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, render.ErrUnknownModel):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		h.Log.Warn("preview failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// updateModule runs one update now and returns to the index. The outcome
// shows in the module table.
func (h *Handler) updateModule(w http.ResponseWriter, r *http.Request) {
	err := h.Modules.Update(r.Context(), r.PathValue("module"))
	if errors.Is(err, modules.ErrUnknownModule) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.Log.Warn("manual update failed", "module", r.PathValue("module"), "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
