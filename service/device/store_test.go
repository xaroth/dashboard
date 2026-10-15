package device_test

import (
	"testing"

	"github.com/xaroth/dashboard/service/device"
)

func TestStore(t *testing.T) {
	t.Parallel()
	s := &device.Store{Max: 2}
	s.Put("a", []byte("a"))
	s.Put("b", []byte("b"))
	s.Put("a", []byte("a")) // refreshes a, so b is now the oldest
	s.Put("c", []byte("c"))

	testCases := []struct {
		name string
		want bool
	}{
		{"a", true},
		{"b", false},
		{"c", true},
	}
	for _, testCase := range testCases {
		if _, ok := s.Get(testCase.name); ok != testCase.want {
			t.Errorf("Get(%q): got %v, want %v", testCase.name, ok, testCase.want)
		}
	}
}
