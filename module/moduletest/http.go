// Package moduletest helps test modules.
package moduletest

import (
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"time"
)

// HTTPClient returns a service HTTP client that serves every request
// in-process with handler, whatever its host, for module.Options. Unlike
// httptest.Server it works inside a synctest bubble. Requests report
// httptrace GotConn before reaching handler.
func HTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: fakeTransport{handler}}
}

type fakeTransport struct{ handler http.Handler }

func (f fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.GotConn != nil {
		trace.GotConn(httptrace.GotConnInfo{})
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}
