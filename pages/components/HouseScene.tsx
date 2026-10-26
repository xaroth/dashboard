import { formatNumber } from "../lib/format";
import type { CarStatus } from "../lib/home";
import styles from "./HouseScene.module.css";
import { StaleMark } from "./StaleMark";

// In px; the scene is drawn for this box and HouseScene.module.css places the values in it.
export const SCENE_WIDTH = 330;
export const SCENE_HEIGHT = 272;

export type HouseSceneProps = {
  /** Solar power now in W, and its energy today in kWh. */
  solarW: number | null;
  solarKwh: number | null;
  /** The sun is down: no production shows a moon instead of the panels. */
  night: boolean;
  /** Today's kWh taken from and returned to the net, and the net flow now in kW, + taken. */
  takenKwh: number | null;
  returnedKwh: number | null;
  flowKw: number | null;
  /** Without a car the right of the scene stays empty. */
  car: CarStatus | null;
  showCar: boolean;
  /** Which values are stale or missing, each marked with a "!". */
  stale: { solar: boolean; meter: boolean; car: boolean };
};

const kwh = (v: number | null) => (v == null ? "-" : formatNumber(v, 1));

function Arrow({ up }: { up: boolean }) {
  return (
    <svg width={24} height={34} viewBox="0 0 24 34" className={styles.arrow}>
      <path
        d={up ? "M12 30V6M4 14l8-9 8 9" : "M12 4v24M4 20l8 9 8-9"}
        fill="none"
        stroke="#000"
        strokeWidth={3.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * The house with solar power on its roof and today's energy taken and
 * returned inside, and the car beside it with its battery level; a cable
 * runs to the car while it is plugged in.
 */
export function HouseScene({ solarW, solarKwh, night, takenKwh, returnedKwh, flowKw, car, showCar, stale }: HouseSceneProps) {
  // Under 10 W is inverter standby, not production.
  const producing = solarW != null && solarW >= 10;
  const flow =
    flowKw == null ? null : `nu ${formatNumber(Math.abs(flowKw), 1)} kW ${Math.round(flowKw * 10) < 0 ? "terug" : "afname"}`;
  const level = car?.level ?? null;
  return (
    <div className={styles.scene} style={{ width: SCENE_WIDTH, height: SCENE_HEIGHT }}>
      <svg width={SCENE_WIDTH} height={SCENE_HEIGHT} viewBox={`0 0 ${SCENE_WIDTH} ${SCENE_HEIGHT}`} className={styles.drawing}>
        <line x1={0} y1={262} x2={SCENE_WIDTH} y2={262} stroke="#000" strokeWidth={2} />
        <path d="M14 112L114 22L214 112Z" fill="#fff" stroke="#000" strokeWidth={4} strokeLinejoin="round" />
        <rect x={30} y={112} width={168} height={150} fill="#fff" stroke="#000" strokeWidth={4} />
        {showCar && car?.pluggedIn ? (
          <path d="M198 232h6q8 0 8-8v-24" fill="none" stroke="#000" strokeWidth={3} strokeDasharray="6 3" />
        ) : null}
        {showCar ? (
          <g transform="translate(214 176)">
            <path
              d="M4 62v-18q0-8 8-10l14-4 14-16q4-4 10-4h28q6 0 10 5l12 16q10 2 10 12v19z"
              fill="#fff"
              stroke="#000"
              strokeWidth={3.5}
              strokeLinejoin="round"
            />
            <path d="M32 30l11-12h14v12zM63 30v-12h12l9 12z" fill="#aaa" stroke="#000" strokeWidth={2} />
            <circle cx={26} cy={64} r={11} fill="#fff" stroke="#000" strokeWidth={4} />
            <circle cx={26} cy={64} r={3} fill="#000" />
            <circle cx={80} cy={64} r={11} fill="#fff" stroke="#000" strokeWidth={4} />
            <circle cx={80} cy={64} r={3} fill="#000" />
          </g>
        ) : null}
      </svg>

      <div className={producing ? styles.solar : styles.solarIdle}>
        <div className={styles.solarNow}>
          <svg width={28} height={22} viewBox="0 0 28 22">
            {!producing && night ? (
              <path d="M15 1a10 10 0 1 0 6 16A8.5 8.5 0 0 1 15 1z" fill="#555" />
            ) : (
              <g fill={producing ? "#555" : "#aaa"} stroke={producing ? "#fff" : "#555"} strokeWidth={1}>
                <rect x={1} y={1} width={13} height={10} />
                <rect x={14} y={1} width={13} height={10} />
                <rect x={1} y={11} width={13} height={10} />
                <rect x={14} y={11} width={13} height={10} />
              </g>
            )}
          </svg>
          <span className={styles.solarValue}>{solarW == null ? "-" : producing ? formatNumber(solarW / 1000, 1) : "0"}</span>
          {solarW != null ? <span className={styles.unit}>{producing ? "kW" : "W"}</span> : null}
        </div>
        {solarKwh != null ? <div className={styles.solarDay}>vandaag {kwh(solarKwh)} kWh</div> : null}
      </div>
      {stale.solar ? <StaleMark className={styles.staleSolar} /> : null}

      <div className={styles.taken}>
        <Arrow up={false} />
        <div className={styles.energy}>
          <div>
            <span className={styles.energyValue}>{kwh(takenKwh)}</span>
            {takenKwh != null ? <span className={styles.unit}>kWh</span> : null}
          </div>
          <span className={styles.energyLabel}>afname</span>
        </div>
      </div>
      <div className={styles.returned}>
        <Arrow up />
        <div className={styles.energy}>
          <div>
            <span className={styles.energyValue}>{kwh(returnedKwh)}</span>
            {returnedKwh != null ? <span className={styles.unit}>kWh</span> : null}
          </div>
          <span className={styles.energyLabel}>teruglevering</span>
        </div>
      </div>
      {flow ? <div className={styles.flow}>{flow}</div> : null}
      {stale.meter ? <StaleMark className={styles.staleMeter} /> : null}

      {showCar ? (
        <>
          <div className={styles.battery}>
            <div className={styles.batteryBody}>
              <div className={styles.batteryFill} style={{ width: `${Math.round(Math.min(100, Math.max(0, level?.percent ?? 0)))}%` }} />
              <div className={styles.batteryText}>
                {car?.charging ? (
                  <svg width={14} height={22} viewBox="0 0 14 22">
                    <path d="M9 0L1 13h5l-2 9 9-13H8l3-9z" fill="#000" />
                  </svg>
                ) : null}
                <span>{level ? `${Math.round(level.percent)}%` : "-"}</span>
              </div>
            </div>
            <div className={styles.batteryTip} />
          </div>
          {car?.detail ? <div className={styles.carDetail}>{car.detail}</div> : null}
          {stale.car ? <StaleMark className={styles.staleCar} /> : null}
        </>
      ) : null}
    </div>
  );
}
