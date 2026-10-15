// The frame codec is unexported; these tests pin its bytes to renderer/frame.ts.
package render

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Shared with renderer/frame.test.ts, which checks the same bytes.
const (
	// encodeFrame({id: 1, op: "describe"})
	vectorRequest = "000000187b226964223a312c226f70223a226465736372696265227d"
	// encodeFrame({id: 2, hash: "h"}, [1, 2, 3])
	vectorResponse = "0000001b7b226964223a322c2268617368223a2268222c22706e67223a337d010203"
)

func TestFrameRequestVector(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeFrame(&buf, []byte(`{"id":1,"op":"describe"}`)); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(buf.Bytes()); got != vectorRequest {
		t.Errorf("got %s, want %s", got, vectorRequest)
	}
}

func TestReadFrame(t *testing.T) {
	t.Parallel()
	vector, _ := hex.DecodeString(vectorResponse)
	testCases := []struct {
		name     string
		in       []byte
		wantID   int64
		wantBody []byte
	}{
		{"shared vector", vector, 2, []byte{1, 2, 3}},
		{"empty png", append([]byte{0, 0, 0, 16}, `{"id":1,"png":0}`...), 1, []byte{}},
		{"no png", append([]byte{0, 0, 0, 8}, `{"id":3}`...), 3, nil},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			resp, body, err := readFrame(bytes.NewReader(testCase.in))
			if err != nil {
				t.Fatalf("got error %v", err)
			}
			if resp.ID != testCase.wantID {
				t.Errorf("got id %d, want %d", resp.ID, testCase.wantID)
			}
			if diff := cmp.Diff(testCase.wantBody, body); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReadFrameErrors(t *testing.T) {
	t.Parallel()
	vector, _ := hex.DecodeString(vectorResponse)
	testCases := []struct {
		name string
		in   []byte
		want error
		msg  string
	}{
		{"empty stream", nil, io.EOF, ""},
		{"short header", vector[:10], io.ErrUnexpectedEOF, ""},
		{"short body", vector[:len(vector)-1], io.ErrUnexpectedEOF, ""},
		{"header too large", []byte{0x7f, 0, 0, 0}, nil, "too large"},
		{"bad json", []byte{0, 0, 0, 1, '{'}, nil, "decoding"},
		{"negative png", append([]byte{0, 0, 0, 10}, `{"png":-1}`...), nil, "out of range"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := readFrame(bytes.NewReader(testCase.in))
			if testCase.want != nil && !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
			if testCase.msg != "" && (err == nil || !strings.Contains(err.Error(), testCase.msg)) {
				t.Errorf("got %v, want error containing %q", err, testCase.msg)
			}
		})
	}
}
