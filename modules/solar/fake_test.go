package solar

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

var fakes sync.Map // address to *FakeDTU

func init() {
	dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		f, ok := fakes.Load(addr)
		if !ok {
			return nil, errors.New("connection refused")
		}
		client, server := net.Pipe()
		go f.(*FakeDTU).serve(server)
		return client, nil
	}
}

// FakeDTU answers Modbus FC03 like a DTU-Pro-S, in-process. It works inside
// a synctest bubble.
type FakeDTU struct {
	Host string

	mu        sync.Mutex
	inputs    [][]byte
	badCount  bool
	hang      bool
	exception bool
	requests  int
}

// NewFakeDTU serves a DTU without inverters at Host, port 502.
func NewFakeDTU(tb testing.TB) *FakeDTU {
	f := &FakeDTU{Host: "dtu-" + strings.NewReplacer("/", "-", " ", "-").Replace(tb.Name())}
	addr := net.JoinHostPort(f.Host, "502")
	fakes.Store(addr, f)
	tb.Cleanup(func() { fakes.Delete(addr) })
	return f
}

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

// SetWrongByteCount makes responses carry a wrong PDU byte count, as some
// DTUs send.
func (f *FakeDTU) SetWrongByteCount(v bool) { f.mu.Lock(); f.badCount = v; f.mu.Unlock() }

// SetHang makes the DTU read requests without answering.
func (f *FakeDTU) SetHang(v bool) { f.mu.Lock(); f.hang = v; f.mu.Unlock() }

// SetException makes the DTU answer exception 2 (illegal address).
func (f *FakeDTU) SetException(v bool) { f.mu.Lock(); f.exception = v; f.mu.Unlock() }

// Requests returns the number of requests answered or hung on.
func (f *FakeDTU) Requests() int { f.mu.Lock(); defer f.mu.Unlock(); return f.requests }

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
