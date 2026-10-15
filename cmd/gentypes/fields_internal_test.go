// jsonFields is unexported in package main; it must agree with encoding/json.
package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type Emb struct {
	A int `json:"a"`
}

type emb2 struct {
	B int `json:"b"`
}

type C1 struct{ X int }
type C2 struct{ X string }
type T1 struct {
	Y int `json:"y"`
}
type T2 struct{ Y int }

type deep struct{ Z int }
type mid struct{ deep }

type Tricky struct {
	*Emb
	emb2
	C1
	C2
	T1
	T2
	mid
	Z      string // shallower than mid.deep.Z
	Named  Emb    `json:"named"`
	Quoted int    `json:"n,string"`
	Skip   int    `json:"-"`
	Dash   int    `json:"-,"`
	odd    int
}

// keys returns the object keys of v's JSON in order.
func keys(tb testing.TB, v any) []string {
	tb.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		tb.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.Token() // {
	var out []string
	for dec.More() {
		k, _ := dec.Token()
		out = append(out, k.(string))
		var skip json.RawMessage
		dec.Decode(&skip)
	}
	return out
}

func TestJSONFieldsMatchEncoder(t *testing.T) {
	t.Parallel()
	v := Tricky{Emb: &Emb{}}
	var got []string
	for _, f := range jsonFields(reflect.TypeOf(v)) {
		got = append(got, f.name)
	}
	if diff := cmp.Diff(keys(t, v), got); diff != "" {
		t.Errorf("fields (-encoding/json +gentypes):\n%s", diff)
	}
}
