package solar

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
)

// RealInput is the registers of an input of 138292184456 producing 42.1 W,
// read from a DTU-Pro-S on V00.07.04.
var RealInput = []byte{
	0x0c, 0x13, 0x82, 0x92, 0x18, 0x44, 0x56, 0x01, 0x01, 0x4e, 0x00, 0x7e, 0x09, 0x32, 0x13, 0x86,
	0x01, 0xa5, 0x01, 0x12, 0x00, 0x0c, 0xd7, 0x76, 0x00, 0xbb, 0x00, 0x03, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

// FakeHost is the host a FakeDTU answers at, on port 502.
const FakeHost = "dtu"

// FakeDTU answers Modbus FC03 like a DTU-Pro-S, in-process over net.Pipe,
// so it works inside a synctest bubble. It is the module.Options Dialer.
type FakeDTU struct {
	mu        sync.Mutex
	inputs    [][]byte
	badCount  bool
	hang      bool
	exception bool
	requests  int
}

// FakeOption sets up a FakeDTU.
type FakeOption func(testing.TB, *FakeDTU)

// NewFake returns a DTU without inverters, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeDTU {
	tb.Helper()
	f := &FakeDTU{}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectWrongByteCount makes responses carry a wrong PDU byte count, as
// some DTUs send.
func ExpectWrongByteCount(_ testing.TB, f *FakeDTU) { f.badCount = true }

// ExpectHang makes the DTU read requests without answering.
func ExpectHang(_ testing.TB, f *FakeDTU) { f.hang = true }

// ExpectException makes the DTU answer exception 2 (illegal address).
func ExpectException(_ testing.TB, f *FakeDTU) { f.exception = true }

// FakeInput returns the registers of an input: power in W, with voltage,
// current and counters derived from it.
func FakeInput(serial string, port int, power float64, link bool) []byte {
	d := make([]byte, 40)
	d[0] = 0x0c
	hex.Decode(d[1:7], []byte(serial))
	d[7] = byte(port)
	binary.BigEndian.PutUint16(d[8:], 335)                     // 33.5 V
	binary.BigEndian.PutUint16(d[10:], uint16(power*100/33.5)) // A
	binary.BigEndian.PutUint16(d[12:], 2300)
	binary.BigEndian.PutUint16(d[14:], 5000)
	binary.BigEndian.PutUint16(d[16:], uint16(power*10))
	binary.BigEndian.PutUint16(d[18:], 100)
	binary.BigEndian.PutUint32(d[20:], 500000)
	binary.BigEndian.PutUint16(d[24:], 200)
	binary.BigEndian.PutUint16(d[26:], 3)
	if link {
		d[32] = 1
	}
	return d
}

// SetInputs replaces the inverter inputs, each 40 bytes of registers.
func (f *FakeDTU) SetInputs(inputs ...[]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = inputs
}

// Requests returns the number of requests answered or hung on.
func (f *FakeDTU) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// DialContext connects to the DTU at FakeHost:502 and refuses other addresses.
func (f *FakeDTU) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	if address != net.JoinHostPort(FakeHost, "502") {
		return nil, errors.New("connection refused")
	}
	client, server := net.Pipe()
	go f.serve(server)
	return client, nil
}

func (f *FakeDTU) serve(conn net.Conn) {
	defer conn.Close()
	for {
		var req [12]byte
		if _, err := io.ReadFull(conn, req[:]); err != nil {
			return
		}
		addr, count := binary.BigEndian.Uint16(req[8:]), binary.BigEndian.Uint16(req[10:])
		f.mu.Lock()
		f.requests++
		hang, exception, badCount := f.hang, f.exception, f.badCount
		data := f.registers(addr, count)
		f.mu.Unlock()
		if hang {
			io.Copy(io.Discard, conn)
			return
		}
		pdu := []byte{req[6], 3, byte(len(data))}
		if badCount {
			pdu[2] = 0xff
		}
		pdu = append(pdu, data...)
		if exception {
			pdu = []byte{req[6], 0x83, 2}
		}
		resp := append(req[:4:4], 0, byte(len(pdu)))
		if _, err := conn.Write(append(resp, pdu...)); err != nil {
			return
		}
	}
}

// registers returns count registers from addr; f.mu is held.
func (f *FakeDTU) registers(addr, count uint16) []byte {
	out := make([]byte, int(count)*2)
	switch {
	case addr == dtuSerial:
		copy(out, []byte{0x41, 0x21, 0xa0, 0x08, 0x43, 0x4f})
	case addr >= inputBase && addr < dtuSerial && (addr-inputBase)%inputStride == 0:
		if i := int(addr-inputBase) / inputStride; i < len(f.inputs) {
			copy(out, f.inputs[i])
		}
	}
	return out
}
