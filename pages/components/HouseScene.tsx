import { formatNumber } from "../lib/format";
import type { CarStatus } from "../lib/home";
import styles from "./HouseScene.module.css";
import { StaleMark } from "./StaleMark";

// In px; the scene is drawn for this box and HouseScene.module.css places the values in it.
export const SCENE_WIDTH = 330;
export const SCENE_HEIGHT = 298;

// Where a taller scene's extra height goes: the walls grow up to
// MAX_WALL_GROWTH, the rest moves the house down, leaving room above the roof.
// The meter boxes stay centered in the walls; the ground and car move down.
const MAX_WALL_GROWTH = 10;
const shift = (top: number) => ({ position: "absolute", left: 0, top, width: SCENE_WIDTH, height: SCENE_HEIGHT }) as const;

export type HouseSceneProps = {
  /** At least SCENE_HEIGHT. */
  height: number;
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
 * runs to the car while it is plugged in at home.
 */
export function HouseScene({ height, solarW, solarKwh, night, takenKwh, returnedKwh, flowKw, car, showCar, stale }: HouseSceneProps) {
  // Under 10 W is inverter standby, not production.
  const producing = solarW != null && solarW >= 10;
  const flow =
    flowKw == null ? null : `nu ${formatNumber(Math.abs(flowKw), 1)} kW ${Math.round(flowKw * 10) < 0 ? "terug" : "afname"}`;
  const level = car?.level ?? null;
  const dy = Math.max(0, height - SCENE_HEIGHT);
  const wallDy = Math.min(dy, MAX_WALL_GROWTH);
  const roofDy = dy - wallDy;
  return (
    <div className={styles.scene} style={{ width: SCENE_WIDTH, height: SCENE_HEIGHT + dy }}>
      <svg width={SCENE_WIDTH} height={SCENE_HEIGHT + dy} viewBox={`0 0 ${SCENE_WIDTH} ${SCENE_HEIGHT + dy}`} className={styles.drawing}>
        <line x1={0} y1={288 + dy} x2={SCENE_WIDTH} y2={288 + dy} stroke="#000" strokeWidth={2} />
        <path d={`M14 ${112 + roofDy}L114 ${22 + roofDy}L214 ${112 + roofDy}Z`} fill="#fff" stroke="#000" strokeWidth={4} strokeLinejoin="round" />
        <rect x={30} y={112 + roofDy} width={168} height={176 + wallDy} fill="#fff" stroke="#000" strokeWidth={4} />
        {showCar && car?.pluggedIn && !car.away ? (
          <path d={`M198 ${276 + dy}h16`} fill="none" stroke="#000" strokeWidth={3} strokeDasharray="6 3" />
        ) : null}
        {showCar ? (
          // Drawn facing the house, wheels on the ground; mirrored in place to drive away.
          <g transform={car?.away ? `translate(325 ${214 + dy}) scale(-1 1)` : `translate(214 ${214 + dy})`}>
            <path
              d="M2 64L2 54Q2 49.5 4.5 48.5L31 43.4L48 35.2Q50.5 33 54 32.8L69 32.2L90 33L100 35L98.5 36.8L106.5 43.4L108.5 50L109.4 57V63.5L107.5 65.6H102.7A12 12 0 0 0 78.9 65.6H32.9A12 12 0 0 0 9.1 65.6H5Z"
              fill="#fff"
              stroke="#000"
              strokeWidth={3}
              strokeLinejoin="round"
            />
            <path
              d="M36 45L53 36.3Q54 35.5 56 35.5H62.5V43.6ZM65 35.5H84L92 36.5L86 41.6L65 43.3Z"
              fill="#aaa"
              stroke="#000"
              strokeWidth={1.8}
              strokeLinejoin="round"
            />
            <path d="M86 41.6L99.5 36" stroke="#555" strokeWidth={2} strokeLinecap="round" />
            <path d="M3.5 49.6L12 48.6" stroke="#000" strokeWidth={2} strokeLinecap="round" />
            {[21, 90.8].map((x) => (
              <g key={x}>
                <circle cx={x} cy={63.8} r={9.3} fill="#fff" stroke="#000" strokeWidth={3} />
                <circle cx={x} cy={63.8} r={2.5} fill="#000" />
              </g>
            ))}
          </g>
        ) : null}
      </svg>

      <div style={shift(roofDy)}>
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
      </div>

      <div style={shift(roofDy + wallDy / 2)}>
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
        {stale.meter ? <StaleMark className={styles.staleMeter} /> : null}
      </div>

      <div style={shift(dy)}>
        {flow ? <div className={styles.flow}>{flow}</div> : null}
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
    </div>
  );
}
