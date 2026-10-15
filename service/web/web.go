// Package web serves the admin UI: status and previews of what devices get.
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/xaroth/dashboard/service/render"
)

// Device is a configured device as the admin UI shows it.
type Device struct {
	Name     string
	Pages    []string
	Rotation int
}

type Pipeline interface {
	Pages() []string
	Props(ctx context.Context, t render.Target, page string) (render.Props, error)
	Render(ctx context.Context, t render.Target, page string) (render.Image, error)
}

type Handler struct {
	Pipeline Pipeline
	Devices  []Device
	// Target returns how a device is rendered: its last reported model and
	// wake, or a default model when it has not connected yet.
	Target func(device string) (render.Target, bool)
	Log    *slog.Logger
}

const defaultModel = "reterminal_e1001"

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.index)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := indexTmpl.Execute(w, map[string]any{
		"Devices": h.Devices,
		"Pages":   h.Pipeline.Pages(),
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
		t.Model = defaultModel
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(props)
}

// target resolves the device in the path; ?model= overrides its model.
func (h *Handler) target(r *http.Request) (render.Target, bool) {
	t, ok := h.Target(r.PathValue("device"))
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
		h.Log.Warn("preview failed", "target", t.Name, "page", page, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(img.PNG)
}
