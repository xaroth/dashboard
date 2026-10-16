package device_test

import (
	"testing"

	"github.com/xaroth/dashboard/device"
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
		key  string
		want bool
	}{
		{"refreshed", "a", true},
		{"evicted", "b", false},
		{"newest", "c", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := s.Get(testCase.key); ok != testCase.want {
				t.Errorf("got %v, want %v", ok, testCase.want)
			}
		})
	}
}
