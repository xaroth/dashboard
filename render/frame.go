package render

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxHeader = 16 << 20

// writeFrame writes a 4-byte big-endian length, then the JSON header.
func writeFrame(w io.Writer, b []byte) error {
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

// response is the union of all response headers.
type response struct {
	ID    int64           `json:"id"`
	Error string          `json:"error"`
	PNG   *int            `json:"png"`
	Pages json.RawMessage `json:"pages"`
}

// readFrame reads one response and, when the header announces one, its PNG body.
func readFrame(r io.Reader) (response, []byte, error) {
	var resp response
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return resp, nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > maxHeader {
		return resp, nil, fmt.Errorf("frame header too large: %d", size)
	}
	header := make([]byte, size)
	if _, err := io.ReadFull(r, header); err != nil {
		return resp, nil, unexpected(err)
	}
	if err := json.Unmarshal(header, &resp); err != nil {
		return resp, nil, fmt.Errorf("decoding frame header: %w", err)
	}
	if resp.PNG == nil {
		return resp, nil, nil
	}
	if *resp.PNG < 0 || *resp.PNG > maxPNG {
		return resp, nil, fmt.Errorf("png size out of range: %d", *resp.PNG)
	}
	body := make([]byte, *resp.PNG)
	if _, err := io.ReadFull(r, body); err != nil {
		return resp, nil, unexpected(err)
	}
	return resp, body, nil
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
