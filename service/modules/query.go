package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type query struct {
	moduleType reflect.Type
	params     reflect.Type
	result     reflect.Type
	call       func(ctx context.Context, m any, params []byte) (any, error)
	check      func(params []byte) error
}

// WithQuery exposes fn as the module's query name. Pages ask for it as
// "module.name" with params that decode into P; the JSON result R reaches
// the page. fn is a method expression, e.g. (*module).forecast.
func WithQuery[M any, P any, R any](name string, fn func(*M, context.Context, P) (R, error)) Option {
	decode := func(raw []byte) (P, error) {
		var p P
		dec := json.NewDecoder(bytes.NewReader(cmpBytes(raw, []byte("{}"))))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return p, fmt.Errorf("params: %w", err)
		}
		return p, nil
	}
	q := query{
		moduleType: reflect.TypeFor[*M](),
		params:     reflect.TypeFor[P](),
		result:     reflect.TypeFor[R](),
		call: func(ctx context.Context, m any, raw []byte) (any, error) {
			p, err := decode(raw)
			if err != nil {
				return nil, err
			}
			return fn(m.(*M), ctx, p)
		},
		check: func(raw []byte) error {
			_, err := decode(raw)
			return err
		},
	}
	return func(r *Registration) {
		if !validName.MatchString(name) {
			panic(fmt.Sprintf("modules: %s: invalid query name %q", r.name, name))
		}
		if _, ok := r.queries[name]; ok {
			panic(fmt.Sprintf("modules: %s: query %s defined twice", r.name, name))
		}
		r.queries[name] = q
	}
}

func cmpBytes(b, fallback []byte) []byte {
	if len(b) == 0 || string(b) == "null" {
		return fallback
	}
	return b
}

// Duration decodes "24h"-style strings; time.Duration does not decode from
// JSON strings.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"24h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Result is a query result with the freshness of the module's data.
type Result struct {
	Value     any
	FetchedAt *time.Time
	Stale     bool
	// Error is the query's error, or else the module's last update error.
	Error string
}

var (
	ErrUnknownModule = errors.New("unknown module")
	ErrUnknownQuery  = errors.New("unknown query")
)

// Check reports whether module.query exists and params decode into its params type.
func (s *Set) Check(module, name string, params map[string]any) error {
	inst, q, err := s.lookup(module, name)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := q.check(raw); err != nil {
		return fmt.Errorf("%s.%s: %w", inst.reg.name, name, err)
	}
	return nil
}

// Resolve runs module.query on the read-only connection. Failures are in
// the result, not returned: one failing query must not fail a page.
func (s *Set) Resolve(ctx context.Context, module, name string, params map[string]any) Result {
	inst, q, err := s.lookup(module, name)
	if err != nil {
		return Result{Stale: true, Error: err.Error()}
	}
	res := Result{}
	if st, err := s.state(ctx, inst); err == nil {
		res.FetchedAt, res.Stale, res.Error = st.FetchedAt, st.Stale, st.Error
	}
	raw, err := json.Marshal(params)
	if err == nil {
		res.Value, err = q.call(s.context(ctx, inst, s.db.Reader), inst.module, raw)
	}
	if err != nil {
		res.Value, res.Error = nil, err.Error()
	}
	return res
}

func (s *Set) lookup(module, name string) (*instance, query, error) {
	for _, inst := range s.instances {
		if inst.reg.name != module {
			continue
		}
		q, ok := inst.reg.queries[name]
		if !ok {
			return nil, query{}, fmt.Errorf("%w %s.%s (have %s)", ErrUnknownQuery, module, name, strings.Join(inst.reg.queryNames(), ", "))
		}
		return inst, q, nil
	}
	return nil, query{}, fmt.Errorf("%w %q", ErrUnknownModule, module)
}
