const number0 = new Intl.NumberFormat("nl-NL", { maximumFractionDigits: 0 });
const number1 = new Intl.NumberFormat("nl-NL", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const number2 = new Intl.NumberFormat("nl-NL", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

// Round first, so -0.004 formats as 0 rather than -0.
function fixed(n: number, digits: number): number {
  return Number(n.toFixed(digits)) || 0;
}

/** +1,23 kW taken from the net, -0,45 kW returned to it. */
export function formatFlow(kw: number): string {
  const r = fixed(kw, 2);
  return `${r > 0 ? "+" : ""}${number2.format(r)} kW`;
}

/** 1.234 W */
export function formatWatt(w: number): string {
  return `${number0.format(fixed(w, 0))} W`;
}

/** 12,3 kWh */
export function formatKwh(kwh: number): string {
  return `${number1.format(fixed(kwh, 1))} kWh`;
}

/** 1,23 m³; "-" while gas is unknown. */
export function formatGas(m3: number | null): string {
  return m3 == null ? "-" : `${number2.format(fixed(m3, 2))} m³`;
}

/** 230,1 V; "-" for a phase the meter does not report. */
export function formatVolt(v: number | null): string {
  return v == null ? "-" : `${number1.format(fixed(v, 1))} V`;
}

/** 456.1: the inverter serial's last three digits and the input. */
export function panelLabel(inverter: string, port: number): string {
  return `${inverter.slice(-3)}.${port}`;
}
