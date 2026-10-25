package main

import (
	"reflect"

	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/screen"
)

// propsTypes are what a page component receives. Data and a result's value
// are typed by the page's queries, in pages/lib/page.ts.
var propsTypes = []tsType{
	{t: reflect.TypeFor[render.Props](), name: "Props<D>", fields: map[string]string{"data": "D"}},
	{t: reflect.TypeFor[render.Result](), name: "Result<T>", fields: map[string]string{"value": "T | null"}},
	{t: reflect.TypeFor[render.DeviceProps](), name: "Device"},
	{t: reflect.TypeFor[render.Viewport](), name: "Viewport"},
	{t: reflect.TypeFor[render.SystemProps](), name: "System"},
	{t: reflect.TypeFor[screen.Wake](), name: "Wake"},
}

var propsEnums = map[reflect.Type][]string{
	reflect.TypeFor[screen.PowerSource](): enumValues(screen.PowerSources),
}

func enumValues[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}
