package eveonline

import (
	"net/http"
	"net/http/httptest"
	"time"
)

// NewFakeClient returns a service HTTP client that serves every request
// in-process with handler, for tests of modules using ESI. Unlike
// httptest.Server it works inside a synctest bubble.
func NewFakeClient(handler http.Handler) *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: fakeTransport{handler}}
}

type fakeTransport struct{ handler http.Handler }

func (f fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}
