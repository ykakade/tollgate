import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';

// useWidth returns the width of the element, and follows its changes.
function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(600);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setWidth(Math.round(entry.contentRect.width));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

// niceScale returns a round axis maximum at or above v, and the step of its
// ticks. The step is at least minStep, so counts get whole-number ticks.
export function niceScale(v: number, ticks = 4, minStep = 0): { max: number; step: number } {
  if (!(v > 0)) return { max: Math.max(1, minStep), step: Math.max(0.25, minStep) };
  const raw = v / ticks;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const norm = raw / mag;
  const step = Math.max((norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag, minStep);
  return { max: step * Math.ceil(v / step), step };
}

// usdTicks returns a formatter for axis ticks in micro-USD, with the
// fewest decimals that show each step exactly.
export function usdTicks(step: number): (v: number) => string {
  const usdStep = step / 1e6;
  let digits = 0;
  while (digits < 6 && Math.abs(Math.round(usdStep * 10 ** digits) - usdStep * 10 ** digits) > 1e-9) digits++;
  return (v) => `$${(v / 1e6).toFixed(digits)}`;
}

export interface Series {
  name: string;
  color: string;
}

interface TooltipRow {
  color: string;
  value: string;
  name: string;
  line?: boolean;
}

function Tooltip({ x, y, width, title, rows }: { x: number; y: number; width: number; title: string; rows: TooltipRow[] }) {
  const left = x > width - 180 ? x - 172 : x + 12;
  return (
    <div className="tooltip" style={{ left, top: y }} role="status">
      <div className="t-title">{title}</div>
      {rows.map((r) => (
        <div className="t-row" key={r.name}>
          <span className={r.line ? 'line-key' : 'swatch'} style={{ background: r.color }} />
          <strong>{r.value}</strong>
          <span>{r.name}</span>
        </div>
      ))}
    </div>
  );
}

export function Legend({ series, lines = false }: { series: Series[]; lines?: boolean }) {
  return (
    <div className="legend">
      {series.map((s) => (
        <span key={s.name}>
          <span className={lines ? 'line-key' : 'swatch'} style={{ background: s.color }} />
          {s.name}
        </span>
      ))}
    </div>
  );
}

// roundedTop returns the path of a column with a 4px rounded data end and
// a square base.
function roundedTop(x: number, y: number, w: number, h: number, r = 4): string {
  const rr = Math.min(r, h, w / 2);
  return `M${x} ${y + h}V${y + rr}Q${x} ${y} ${x + rr} ${y}H${x + w - rr}Q${x + w} ${y} ${x + w} ${y + rr}V${y + h}Z`;
}

export interface Column {
  key: string;
  label: string;
  values: number[];
}

// ColumnChart draws one column for each time bucket. More than one series
// stack, with a 2px surface gap between the segments.
export function ColumnChart({
  columns,
  series,
  format,
  height = 200,
  axisLabelEvery,
  minStep,
  ticks: tickMaker,
}: {
  columns: Column[];
  series: Series[];
  format: (v: number) => string;
  height?: number;
  axisLabelEvery?: number;
  // minStep is the smallest step between two axis ticks.
  minStep?: number;
  // ticks, if set, makes the formatter of the axis ticks from the step.
  ticks?: (step: number) => (v: number) => string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const left = 52;
  const bottom = 22;
  const plotW = Math.max(width - left - 4, 50);
  const plotH = height - bottom - 8;
  const totals = columns.map((c) => c.values.reduce((a, b) => a + b, 0));
  const { max, step } = niceScale(Math.max(...totals, 0), 4, minStep);
  const band = plotW / Math.max(columns.length, 1);
  const barW = Math.max(Math.min(24, band * 0.7), 2);
  const y = (v: number) => 8 + plotH - (v / max) * plotH;
  const every = axisLabelEvery ?? Math.max(1, Math.ceil(columns.length / Math.max(Math.floor(plotW / 64), 1)));
  const tickText = tickMaker ? tickMaker(step) : format;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);

  return (
    <div className="chart" ref={ref}>
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Column chart">
        {ticks.map((v) => (
          <g key={v}>
            <line className={v === 0 ? 'baseline' : 'gridline'} x1={left} x2={left + plotW} y1={y(v)} y2={y(v)} />
            <text x={left - 8} y={y(v) + 4} textAnchor="end">
              {tickText(v)}
            </text>
          </g>
        ))}
        {columns.map((c, i) => {
          const x = left + i * band + (band - barW) / 2;
          let base = 0;
          const top = series.reduce((last, _, s) => ((c.values[s] ?? 0) > 0 ? s : last), -1);
          return (
            <g key={c.key} className={hover === i ? 'mark-hover' : undefined}>
              {series.map((s, si) => {
                const v = c.values[si] ?? 0;
                if (v <= 0) return null;
                const y0 = y(base);
                base += v;
                const y1 = y(base);
                // The gap separates a segment from the segment below it.
                const h = Math.max(y0 - y1 - (base - v > 0 ? 2 : 0), 1);
                return si === top ? (
                  <path key={s.name} d={roundedTop(x, y1, barW, h)} fill={s.color} />
                ) : (
                  <rect key={s.name} x={x} y={y1} width={barW} height={h} fill={s.color} />
                );
              })}
              {i % every === 0 && (
                <text x={left + i * band + band / 2} y={height - 6} textAnchor="middle">
                  {c.label}
                </text>
              )}
              <rect
                className="hit"
                x={left + i * band}
                y={8}
                width={band}
                height={plotH}
                tabIndex={0}
                aria-label={`${c.label}: ${format(totals[i] ?? 0)}`}
                onPointerEnter={() => setHover(i)}
                onPointerLeave={() => setHover(null)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
              />
            </g>
          );
        })}
      </svg>
      {hover !== null && columns[hover] && (
        <Tooltip
          x={left + hover * band + band / 2}
          y={8}
          width={width}
          title={columns[hover].label}
          rows={[
            ...series.map((s, si) => ({ color: s.color, name: s.name, value: format(columns[hover]?.values[si] ?? 0) })).reverse(),
          ]}
        />
      )}
    </div>
  );
}

export interface StepPoint {
  t: number;
  y: number;
  highlight?: boolean;
  title: string;
  rows: { name: string; value: string }[];
}

// StepChart draws a running total over time, as a step line with a dot for
// each request. Reference lines mark limits.
export function StepChart({
  points,
  refs,
  ticks: tickMaker,
  height = 220,
  color,
  highlightColor,
}: {
  points: StepPoint[];
  refs: { value: number; label: string }[];
  // ticks makes the formatter of the axis ticks from the step.
  ticks: (step: number) => (v: number) => string;
  height?: number;
  color: string;
  highlightColor: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const left = 56;
  const right = 12;
  const bottom = 22;
  const plotW = Math.max(width - left - right, 50);
  const plotH = height - bottom - 12;
  const first = points[0];
  const last = points[points.length - 1];
  if (!first || !last) return <div className="empty">No requests.</div>;
  const t0 = first.t;
  const t1 = Math.max(last.t, t0 + 60_000);
  const { max, step } = niceScale(Math.max(last.y, ...refs.map((r) => r.value)) * 1.05);
  const x = (t: number) => left + ((t - t0) / (t1 - t0)) * plotW;
  const y = (v: number) => 12 + plotH - (v / max) * plotH;
  let d = `M${x(t0)} ${y(0)}`;
  for (const p of points) d += `H${x(p.t)}V${y(p.y)}`;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);
  const tickText = tickMaker(step);
  const timeTicks = 5;
  const onMove = (e: React.PointerEvent<SVGRectElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - box.left) / box.width) * plotW + left;
    let best = 0;
    points.forEach((p, i) => {
      if (Math.abs(x(p.t) - px) < Math.abs(x(points[best]!.t) - px)) best = i;
    });
    setHover(best);
  };
  const hp = hover !== null ? points[hover] : undefined;
  return (
    <div className="chart" ref={ref}>
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Spend over time">
        {ticks.map((v) => (
          <g key={v}>
            <line className={v === 0 ? 'baseline' : 'gridline'} x1={left} x2={left + plotW} y1={y(v)} y2={y(v)} />
            <text x={left - 8} y={y(v) + 4} textAnchor="end">
              {tickText(v)}
            </text>
          </g>
        ))}
        {Array.from({ length: timeTicks }, (_, i) => t0 + ((t1 - t0) * i) / (timeTicks - 1)).map((t, i) => (
          <text key={t} x={x(t)} y={height - 6} textAnchor={i === 0 ? 'start' : i === timeTicks - 1 ? 'end' : 'middle'}>
            {new Date(t).toLocaleTimeString([], t1 - t0 < 10 * 60_000 ? { hour: '2-digit', minute: '2-digit', second: '2-digit' } : { hour: '2-digit', minute: '2-digit' })}
          </text>
        ))}
        {refs.map((r) => (
          <g key={r.label}>
            <line x1={left} x2={left + plotW} y1={y(r.value)} y2={y(r.value)} stroke="var(--text-3)" strokeWidth={1} />
            <text className="label-strong" x={left + plotW - 4} y={y(r.value) - 5} textAnchor="end">
              {r.label}
            </text>
          </g>
        ))}
        <path d={d} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        {hp && <line x1={x(hp.t)} x2={x(hp.t)} y1={12} y2={12 + plotH} stroke="var(--axis)" strokeWidth={1} />}
        {points.map((p, i) => (
          <circle
            key={i}
            cx={x(p.t)}
            cy={y(p.y)}
            r={hover === i ? 5.5 : 4}
            fill={p.highlight ? highlightColor : color}
            stroke="var(--surface)"
            strokeWidth={2}
          />
        ))}
        <rect
          className="hit"
          x={left}
          y={0}
          width={plotW}
          height={plotH + 12}
          onPointerMove={onMove}
          onPointerLeave={() => setHover(null)}
        />
      </svg>
      {hp && <Tooltip x={x(hp.t)} y={12} width={width} title={hp.title} rows={hp.rows.map((r) => ({ ...r, color: 'transparent' }))} />}
    </div>
  );
}

export interface RangeRow {
  label: string;
  low: number;
  high: number;
}

// RangeChart draws one horizontal row for each item: a dot at the low
// value and a line to the high value, for example TTFT p50 and p95.
export function RangeChart({
  rows,
  format,
  color,
  lowName,
  highName,
}: {
  rows: RangeRow[];
  format: (v: number) => string;
  color: string;
  lowName: string;
  highName: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const labelW = Math.min(220, width * 0.4);
  const rowH = 30;
  const top = 6;
  const height = top + rows.length * rowH + 24;
  const plotW = Math.max(width - labelW - 56, 40);
  const { max, step } = niceScale(Math.max(...rows.map((r) => r.high), 1));
  const x = (v: number) => labelW + (v / max) * plotW;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);
  return (
    <div className="chart" ref={ref}>
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${lowName} and ${highName} for each item`}>
        {ticks.map((v) => (
          <g key={v}>
            <line className={v === 0 ? 'baseline' : 'gridline'} x1={x(v)} x2={x(v)} y1={top} y2={top + rows.length * rowH} />
            <text x={x(v)} y={height - 6} textAnchor="middle">
              {format(v)}
            </text>
          </g>
        ))}
        {rows.map((r, i) => {
          const cy = top + i * rowH + rowH / 2;
          return (
            <g key={r.label}>
              <text className="label-strong" x={0} y={cy + 4}>
                {r.label.length > 30 ? `${r.label.slice(0, 29)}…` : r.label}
              </text>
              <line x1={x(r.low)} x2={x(r.high)} y1={cy} y2={cy} stroke={color} strokeWidth={2} strokeLinecap="round" />
              <line x1={x(r.high)} x2={x(r.high)} y1={cy - 5} y2={cy + 5} stroke={color} strokeWidth={2} strokeLinecap="round" />
              <circle cx={x(r.low)} cy={cy} r={hover === i ? 6 : 4.5} fill={color} stroke="var(--surface)" strokeWidth={2} />
              <text x={x(r.high) + 8} y={cy + 4}>
                {format(r.high)}
              </text>
              <rect
                className="hit"
                x={0}
                y={cy - rowH / 2}
                width={width}
                height={rowH}
                tabIndex={0}
                aria-label={`${r.label}: ${lowName} ${format(r.low)}, ${highName} ${format(r.high)}`}
                onPointerEnter={() => setHover(i)}
                onPointerLeave={() => setHover(null)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
              />
            </g>
          );
        })}
      </svg>
      {hover !== null && rows[hover] && (
        <Tooltip
          x={x(rows[hover].low)}
          y={top + hover * rowH + rowH}
          width={width}
          title={rows[hover].label}
          rows={[
            { color, name: lowName, value: format(rows[hover].low) },
            { color, name: highName, value: format(rows[hover].high), line: true },
          ]}
        />
      )}
    </div>
  );
}

// ChartCard holds a chart, its title and its table view.
export function ChartCard({
  title,
  sub,
  legend,
  chart,
  table,
}: {
  title: string;
  sub?: string;
  legend?: ReactNode;
  chart: ReactNode;
  table: ReactNode;
}) {
  const [showTable, setShowTable] = useState(false);
  return (
    <section className="card">
      <div className="card-head">
        <div>
          <h2>{title}</h2>
          {sub && <p className="sub">{sub}</p>}
        </div>
        <button className="link-button" onClick={() => setShowTable(!showTable)} aria-pressed={showTable}>
          {showTable ? 'Show chart' : 'Show table'}
        </button>
      </div>
      {showTable ? (
        <div className="table-wrap">{table}</div>
      ) : (
        <>
          {legend}
          {chart}
        </>
      )}
    </section>
  );
}
