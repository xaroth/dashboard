package device

import (
	"slices"
	"sync"
)

// Store keeps the most recently put images in memory by filename. The zero
// value keeps one.
type Store struct {
	Max int

	mu    sync.Mutex
	order []string
	byKey map[string][]byte
}

func (s *Store) Put(filename string, png []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey == nil {
		s.byKey = map[string][]byte{}
	}
	if _, ok := s.byKey[filename]; ok {
		s.order = slices.DeleteFunc(s.order, func(k string) bool { return k == filename })
	}
	s.byKey[filename] = png
	s.order = append(s.order, filename)
	for len(s.order) > max(s.Max, 1) {
		delete(s.byKey, s.order[0])
		s.order = s.order[1:]
	}
}

func (s *Store) Get(filename string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	png, ok := s.byKey[filename]
	return png, ok
}
