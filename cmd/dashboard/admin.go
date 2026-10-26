package main

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

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/screen"
)

type Pipeline interface {
	Pages(ctx context.Context) ([]string, error)
	Props(ctx context.Context, t screen.Target, page string) (render.Props, error)
	Render(ctx context.Context, t screen.Target, page string) (screen.Image, error)
}

type Devices interface {
	Statuses() []device.Status
	Pending() []device.Pending
	Target(name string) (screen.Target, bool)
}

type Modules interface {
	Statuses(ctx context.Context) ([]module.Status, error)
	Update(ctx context.Context, name string) error
	Shared(ctx context.Context) []module.SharedStatus
}

// adminHandler serves the admin UI: status and previews of what devices get.
type adminHandler struct {
	Pipeline Pipeline
	Devices  Devices
	Modules  Modules
	// Simulator serves the device protocol for simulated wakes, in-process,
	// on devices of its own; never the device listener's.
	Simulator http.Handler
	Log       *slog.Logger

	simMu   sync.Mutex
	simKeys map[string]string // device name -> api_key from setup
}

// adminPrefixes are the first path segments Register uses; a module page
// under one would shadow part of it.
var adminPrefixes = []string{"modules", "simulate", "images", "preview"}

func (h *adminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("POST /modules/{module}/update", h.updateModule)
	mux.HandleFunc("GET /simulate/{device}", h.simulatePage)
	mux.HandleFunc("POST /simulate/{device}/wake", h.simulateWake)
	mux.Handle("GET /images/{file}", h.Simulator)
	// A page name is "<layout>/<page>" or a shared "<page>".
	mux.HandleFunc("GET /preview/page/{path...}", h.previewPage)
	mux.HandleFunc("GET /preview/{device}/{path...}", h.previewDevice)
}

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

type panelView struct {
	Model    string
	Rotation int
}

// Value is the panel as ?panel= takes it, e.g. reterminal_e1001-90.
func (p panelView) Value() string { return p.Model + "-" + strconv.Itoa(p.Rotation) }

func (p panelView) Orientation() string {
	switch p.Rotation {
	case 90:
		return "portrait"
	case 180:
		return "landscape, upside down"
	case 270:
		return "portrait, other direction"
	}
	return "landscape"
}

// parsePanel reads a panelView.Value; the model must be known.
func parsePanel(s string) (panelView, bool) {
	i := strings.LastIndexByte(s, '-')
	if i < 0 || !slices.Contains(render.Models(), s[:i]) {
		return panelView{}, false
	}
	rot, err := strconv.Atoi(s[i+1:])
	if err != nil || !validRotation(rot) {
		return panelView{}, false
	}
	return panelView{s[:i], rot}, true
}

func validRotation(rot int) bool { return rot == 0 || rot == 90 || rot == 180 || rot == 270 }

// index previews pages on one panel, picked with ?panel=, since every
// preview is a render. It defaults to the first device's panel.
func (h *adminHandler) index(w http.ResponseWriter, r *http.Request) {
	devices := h.Devices.Statuses()
	var panels []panelView
	for _, m := range render.Models() {
		for _, rot := range []int{0, 90} {
			panels = append(panels, panelView{m, rot})
		}
	}
	panel, ok := parsePanel(r.URL.Query().Get("panel"))
	if !ok {
		panel = panelView{device.DefaultModel, 0}
		if len(devices) > 0 {
			panel = panelView{devices[0].Model, devices[0].Rotation}
		}
	}
	// A device at 180 or 270 defaults to a panel not listed; list it so the
	// select shows what the pages render on.
	if !slices.Contains(panels, panel) {
		panels = append(panels, panel)
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
		"Devices": devices,
		"Pending": h.Devices.Pending(),
		"Modules": mods,
		"Shared":  h.Modules.Shared(r.Context()),
		"Pages":   pages,
		"Panels":  panels,
		"Panel":   panel,
	})
	if err != nil {
		h.Log.Error("rendering index", "err", err)
	}
}

func (h *adminHandler) previewPage(w http.ResponseWriter, r *http.Request) {
	page, ok := pngPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	t := screen.Target{Name: "preview", Model: q.Get("model"), Rotation: 0}
	if t.Model == "" {
		t.Model = device.DefaultModel
	}
	if s := q.Get("rotation"); s != "" {
		rot, err := strconv.Atoi(s)
		if err != nil || !validRotation(rot) {
			http.Error(w, "rotation must be 0, 90, 180 or 270", http.StatusBadRequest)
			return
		}
		t.Rotation = rot
	}
	h.servePNG(w, r, t, page)
}

// previewDevice serves {page}.png and {page}/props.json for the device.
func (h *adminHandler) previewDevice(w http.ResponseWriter, r *http.Request) {
	t, ok := h.target(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if page, ok := strings.CutSuffix(r.PathValue("path"), "/props.json"); ok {
		h.serveProps(w, r, t, page)
		return
	}
	page, ok := pngPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.servePNG(w, r, t, page)
}

func (h *adminHandler) serveProps(w http.ResponseWriter, r *http.Request, t screen.Target, page string) {
	props, err := h.Pipeline.Props(r.Context(), t, page)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(props)
}

// pngPage is the page name from a {path} ending in .png.
func pngPage(r *http.Request) (string, bool) {
	return strings.CutSuffix(r.PathValue("path"), ".png")
}

// target resolves the device in the path; ?model= overrides its model.
func (h *adminHandler) target(r *http.Request) (screen.Target, bool) {
	t, ok := h.Devices.Target(r.PathValue("device"))
	if !ok {
		return t, false
	}
	if m := r.URL.Query().Get("model"); m != "" {
		t.Model = m
	}
	return t, true
}

func (h *adminHandler) servePNG(w http.ResponseWriter, r *http.Request, t screen.Target, page string) {
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
func (h *adminHandler) fail(w http.ResponseWriter, err error) {
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
// shows in the module table. The request context ends with the shutdown
// drain, so an update cannot outlive the service.
func (h *adminHandler) updateModule(w http.ResponseWriter, r *http.Request) {
	err := h.Modules.Update(r.Context(), r.PathValue("module"))
	if errors.Is(err, module.ErrUnknownModule) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.Log.Warn("manual update failed", "module", r.PathValue("module"), "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
