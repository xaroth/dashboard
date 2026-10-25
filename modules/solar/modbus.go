package solar

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// modbus is a Modbus TCP client for function code 3 (read holding
// registers), the only one the DTU needs.
type modbus struct {
	conn net.Conn
	unit byte
	tid  uint16
}

// dial connects to the DTU; tests replace it.
var dial = (&net.Dialer{}).DialContext

func dialModbus(ctx context.Context, addr string, unit byte) (*modbus, error) {
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	return &modbus{conn: conn, unit: unit}, nil
}

func (m *modbus) Close() error { return m.conn.Close() }

// readHolding reads count registers from addr and returns their bytes.
// The frame length comes from the MBAP header, not the PDU's byte count:
// some DTUs send a wrong one.
func (m *modbus) readHolding(addr, count uint16) ([]byte, error) {
	m.tid++
	req := binary.BigEndian.AppendUint16(nil, m.tid)
	req = binary.BigEndian.AppendUint16(req, 0) // protocol
	req = binary.BigEndian.AppendUint16(req, 6) // length of what follows
	req = append(req, m.unit, 3)
	req = binary.BigEndian.AppendUint16(req, addr)
	req = binary.BigEndian.AppendUint16(req, count)
	if _, err := m.conn.Write(req); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}
	var hdr [6]byte
	if _, err := io.ReadFull(m.conn, hdr[:]); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	tid, proto, n := binary.BigEndian.Uint16(hdr[0:]), binary.BigEndian.Uint16(hdr[2:]), binary.BigEndian.Uint16(hdr[4:])
	if tid != m.tid || proto != 0 {
		return nil, fmt.Errorf("response transaction %d protocol %d, want %d and 0", tid, proto, m.tid)
	}
	if n < 3 || n > 256 {
		return nil, fmt.Errorf("response length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(m.conn, body); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if body[1] == 0x83 {
		return nil, fmt.Errorf("exception %d reading %#04x", body[2], addr)
	}
	if body[1] != 3 {
		return nil, fmt.Errorf("response function %d, want 3", body[1])
	}
	data := body[3:]
	if len(data) < int(count)*2 {
		return nil, errors.New("response shorter than the registers asked for")
	}
	return data[:count*2], nil
}
