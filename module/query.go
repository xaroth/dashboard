package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type queryDef struct {
	moduleType reflect.Type
	params     reflect.Type
	result     reflect.Type
	call       func(ctx context.Context, m any, q Query, params []byte) (any, error)
	check      func(params []byte) error
}

// WithQuery exposes fn as the module's query name. Pages ask for it as
// "module.name" with params that decode into P; the JSON result R reaches
// the page. fn is a method expression, e.g. (*module).forecast; it gets a
// Query on the read-only pool.
func WithQuery[M any, P any, R any](name string, fn func(*M, context.Context, Query, P) (R, error)) Option {
	decode := func(raw []byte) (P, error) {
		var p P
		dec := json.NewDecoder(bytes.NewReader(orDefault(raw, []byte("{}"))))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return p, fmt.Errorf("params: %w", err)
		}
		return p, nil
	}
	def := queryDef{
		moduleType: reflect.TypeFor[*M](),
		params:     reflect.TypeFor[P](),
		result:     reflect.TypeFor[R](),
		call: func(ctx context.Context, m any, q Query, raw []byte) (res any, err error) {
			// Queries run in the pipeline's goroutines, outside net/http's recovery.
			defer func() {
				if r := recover(); r != nil {
					res, err = nil, fmt.Errorf("query panicked: %v", r)
				}
			}()
			p, err := decode(raw)
			if err != nil {
				return nil, err
			}
			return fn(m.(*M), ctx, q, p)
		},
		check: func(raw []byte) error {
			_, err := decode(raw)
			return err
		},
	}
	return func(r *Registration) {
		if !validName.MatchString(name) {
			panic(fmt.Sprintf("module: %s: invalid query name %q", r.name, name))
		}
		if _, ok := r.queries[name]; ok {
			panic(fmt.Sprintf("module: %s: query %s defined twice", r.name, name))
		}
		r.queries[name] = def
	}
}

func orDefault(b, fallback []byte) []byte {
	if len(b) == 0 || string(b) == "null" {
		return fallback
	}
	return b
}

// Result is a query result with the freshness of the module's data.
type Result struct {
	Value     any
	FetchedAt *time.Time
	Stale     bool
	// Error is the query's error, or else the module's last update error.
	Error string
}

var ErrUnknownModule = errors.New("unknown module")

// Check reports whether module.query exists and params decode into its params type.
func (s *Set) Check(module, name string, params map[string]any) error {
	inst, q, err := s.lookup(module, name)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encoding params: %w", err)
	}
	if err := q.check(raw); err != nil {
		return fmt.Errorf("%s.%s: %w", inst.reg.name, name, err)
	}
	return nil
}

// Resolve runs module.query on the read-only connection. Failures are in
// the result, not returned: one failing query must not fail a page.
func (s *Set) Resolve(ctx context.Context, module, name string, params map[string]any) Result {
	ctx, span := s.tracer.Start(ctx, module+"."+name, trace.WithAttributes(attribute.String("module", module), attribute.String("query", name)))
	defer span.End()
	inst, q, err := s.lookup(module, name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Result{Stale: true, Error: err.Error()}
	}
	res := Result{}
	if st, err := s.state(ctx, inst); err == nil {
		res.FetchedAt, res.Stale, res.Error = st.FetchedAt, st.Stale, st.Error
	} else {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		res.Stale, res.Error = true, fmt.Sprintf("reading module state: %v", err)
	}
	raw, err := json.Marshal(params)
	if err == nil {
		res.Value, err = q.call(ctx, inst.module, s.query(inst, s.reader), raw)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		res.Value, res.Error = nil, err.Error()
	}
	return res
}

func (s *Set) lookup(module, name string) (*instance, queryDef, error) {
	for _, inst := range s.instances {
		if inst.reg.name != module {
			continue
		}
		q, ok := inst.reg.queries[name]
		if !ok {
			return nil, queryDef{}, fmt.Errorf("unknown query %s.%s (have %s)", module, name, strings.Join(inst.reg.queryNames(), ", "))
		}
		return inst, q, nil
	}
	return nil, queryDef{}, fmt.Errorf("%w %q", ErrUnknownModule, module)
}

// QueryType describes a query's params and result types, for code generation.
type QueryType struct {
	Name   string
	Params reflect.Type
	Result reflect.Type
}

// QueryTypes returns the registration's queries, sorted by name.
func (r Registration) QueryTypes() []QueryType {
	out := make([]QueryType, 0, len(r.queries))
	for _, name := range r.queryNames() {
		q := r.queries[name]
		out = append(out, QueryType{Name: name, Params: q.params, Result: q.result})
	}
	return out
}
