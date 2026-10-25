package module

import (
	"cmp"
	"context"
	"net/http"
	"slices"
)

// adminPage is a module or shared value served on the admin listener.
type adminPage struct {
	name string
	h    http.Handler
	// context returns the request context with the dependencies.
	context func(ctx context.Context) context.Context
}

// adminPages returns the modules and shared values that are http.Handlers,
// by name. A module whose Initialize failed, or a shared value that failed
// to build, is left out.
func (s *Set) adminPages() []adminPage {
	var out []adminPage
	for _, d := range s.sharedOrder {
		h, ok := s.sharedHandler(d)
		if !ok {
			continue
		}
		log := s.log.With("shared", d.sharedName())
		out = append(out, adminPage{name: d.sharedName(), h: h, context: func(ctx context.Context) context.Context {
			return context.WithValue(s.baseContext(ctx, log), keyDatabase, s.writer)
		}})
	}
	for _, inst := range s.instances {
		h, ok := inst.page()
		if !ok {
			continue
		}
		out = append(out, adminPage{name: inst.reg.name, h: h, context: func(ctx context.Context) context.Context {
			ctx, _ = s.context(ctx, inst, s.writer, false)
			return ctx
		}})
	}
	slices.SortFunc(out, func(a, b adminPage) int { return cmp.Compare(a.name, b.name) })
	return out
}

// RegisterAdmin serves the modules and shared values that are http.Handlers
// on mux, each under /<name>/ with the prefix stripped. A module's requests
// carry its own context, as in Update; a shared value's carry Database (the
// writer), Logger, HTTPClient and Clock. Requests run concurrently with
// updates.
func (s *Set) RegisterAdmin(mux *http.ServeMux) {
	for _, p := range s.adminPages() {
		mux.Handle("/"+p.name+"/", http.StripPrefix("/"+p.name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.h.ServeHTTP(w, r.WithContext(p.context(r.Context())))
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
func (i *instance) page() (http.Handler, bool) {
	if i.shared || i.initErr != nil {
		return nil, false
	}
	h, ok := i.module.(http.Handler)
	return h, ok
}
