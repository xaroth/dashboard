# Read-only Modbus TCP FC03 against Hoymiles DTU-Pro-S.
import socket, struct, sys
if len(sys.argv) < 2:
    sys.exit(f'usage: {sys.argv[0]} <dtu-host>')
host = sys.argv[1]

def recv_exact(sock, n):
    buf = b''
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError(f'connection closed after {len(buf)} of {n} bytes')
        buf += chunk
    return buf

def fc03(sock, tid, addr, count, unit=1):
    sock.sendall(struct.pack('>HHHBBHH', tid, 0, 6, unit, 3, addr, count))
    hdr = recv_exact(sock, 8)
    if hdr[7] & 0x80:
        raise RuntimeError(f'exception response {hdr.hex()} code {recv_exact(sock, 1).hex()}')
    return recv_exact(sock, recv_exact(sock, 1)[0])

s = socket.create_connection((host, 502), timeout=5)
print('dtu serial', fc03(s, 1, 0x2000, 3).hex())
tot_p = 0; tot_today = 0
for i in range(24):
    d = fc03(s, i + 2, 0x1000 + i * 40, 20)
    serial = d[1:7].hex()
    if serial == '000000000000':
        break
    port = d[7]
    pv_v, pv_i, g_v, g_f, pv_p, today = struct.unpack('>HHHHHH', d[8:20])
    total, = struct.unpack('>I', d[20:24])
    temp, status, alarm, alarm_cnt = struct.unpack('>hHHH', d[24:32])
    link = d[32]
    print(f'{serial} p{port} {pv_v/10:6.1f}V {pv_i/100:5.2f}A {pv_p/10:6.1f}W today={today}Wh total={total/1000:.1f}kWh grid={g_v/10}V {g_f/100}Hz temp={temp/10}C status={status} alarm={alarm} link={link}')
    if link:
        tot_p += pv_p / 10; tot_today += today
print(f'plant: {tot_p:.1f} W, today {tot_today} Wh')
