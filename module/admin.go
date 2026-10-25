package module

import (
	"cmp"
	"net/http"
	"slices"
)

// AdminHandler is a module or shared value with a page on the admin
// listener. q is on the writer: pages change settings the module keeps in
// its tables.
type AdminHandler interface {
	ServeAdmin(w http.ResponseWriter, r *http.Request, q Query)
}

// adminPage is a module or shared value served on the admin listener.
type adminPage struct {
	name  string
	h     AdminHandler
	query func() Query
}

// adminPages returns the modules and shared values that are AdminHandlers,
// by name. A module whose Initialize failed, or a shared value that failed
// to build, is left out.
func (s *Set) adminPages() []adminPage {
	var out []adminPage
	for _, d := range s.sharedOrder {
		h, ok := s.sharedHandler(d)
		if !ok {
			continue
		}
		q := s.sharedQuery(d, s.writer)
		out = append(out, adminPage{name: d.sharedName(), h: h, query: func() Query { return q }})
	}
	for _, inst := range s.instances {
		h, ok := inst.page()
		if !ok {
			continue
		}
		out = append(out, adminPage{name: inst.reg.name, h: h, query: func() Query { return s.query(inst, s.writer) }})
	}
	slices.SortFunc(out, func(a, b adminPage) int { return cmp.Compare(a.name, b.name) })
	return out
}

// RegisterAdmin serves the modules and shared values that are AdminHandlers
// on mux, each under /<name>/ with the prefix stripped. A module's page gets
// its Query with its shared values; a shared value's gets one without.
// Requests run concurrently with updates.
func (s *Set) RegisterAdmin(mux *http.ServeMux) {
	for _, p := range s.adminPages() {
		mux.Handle("/"+p.name+"/", http.StripPrefix("/"+p.name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.h.ServeAdmin(w, r, p.query())
		})))
	}
}

// AdminPages returns the names RegisterAdmin serves, sorted.
func (s *Set) AdminPages() []string {
	out := []string{}
	for _, p := range s.adminPages() {
		out = append(out, p.name)
	}
	return out
}

// page returns the module's admin page. A scheduled shared value is an
// instance too, but its page is the shared value's.
func (i *instance) page() (AdminHandler, bool) {
	if i.shared || i.initErr != nil {
		return nil, false
	}
	h, ok := i.module.(AdminHandler)
	return h, ok
}
