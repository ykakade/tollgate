import { get, type SessionResponse, type SessionsResponse } from '../api';
import { ChartCard, Legend, StepChart, usdTicks } from '../components/charts';
import { ErrorNotice, Meter, Tile } from '../components/ui';
import { ago, cacheReadShare, count, dateTime, minutes, ms, percent, shortSession, time, usd } from '../format';
import { usePoll, type Range } from '../hooks';

function sessionHref(tenant: string, id: string): string {
  return `#/sessions/${encodeURIComponent(tenant)}/${encodeURIComponent(id)}`;
}

export function Sessions({ range, tenant, onUnauthorized }: { range: Range; tenant: string; onUnauthorized: () => void }) {
  const { data, error, loading } = usePoll(
    () => get<SessionsResponse>('/admin/v1/sessions', { from: range.from.toISOString(), to: range.to.toISOString(), tenant, limit: '200' }),
    `${range.name}:${range.from.getTime()}:${tenant}`,
    onUnauthorized,
  );
  if (!data) return error ? <ErrorNotice error={error} /> : <div className="empty">Loading…</div>;
  return (
    <section className={loading ? 'card loading' : 'card'}>
      <h2>Sessions</h2>
      <p className="sub">
        {data.total > data.sessions.length ? `The ${data.sessions.length} most recent of ${data.total} sessions` : `${data.total} sessions`}, the most recent
        first. An <span className="chip">auto</span> session has an ID that Tollgate derived from the prompt prefix.
      </p>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Session</th>
              <th>Tenant</th>
              <th className="num">Requests</th>
              <th className="num">Spend</th>
              <th>Session budget</th>
              <th className="num">Cache read</th>
              <th className="num">Switches</th>
              <th>Last target</th>
              <th>Last request</th>
            </tr>
          </thead>
          <tbody>
            {data.sessions.map((s) => (
              <tr
                key={`${s.tenant_id}/${s.session_id}`}
                className="clickable"
                onClick={() => (window.location.hash = sessionHref(s.tenant_id, s.session_id).slice(1))}
              >
                <td>
                  <a className="mono" href={sessionHref(s.tenant_id, s.session_id)} onClick={(e) => e.stopPropagation()}>
                    {shortSession(s.session_id)}
                  </a>{' '}
                  {s.derived && <span className="chip">auto</span>}
                </td>
                <td>{s.tenant_id}</td>
                <td className="num">{count(s.requests)}</td>
                <td className="num">{usd(s.cost_micro_usd)}</td>
                <td>
                  {s.budget ? (
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                      <Meter ratio={s.budget.used_ratio} warnRatio={s.budget.warn_ratio} label="Session budget used" />
                      <span className="secondary">{percent(s.budget.used_ratio)}</span>
                    </div>
                  ) : (
                    <span className="secondary">none</span>
                  )}
                </td>
                <td className="num">{percent(cacheReadShare(s))}</td>
                <td className="num">{count(s.target_switches)}</td>
                <td className="mono">{s.provider ? `${s.provider}/${s.model}` : '-'}</td>
                <td className="secondary">{ago(s.last)}</td>
              </tr>
            ))}
            {data.sessions.length === 0 && (
              <tr>
                <td colSpan={9} className="empty">
                  No sessions in this time range.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export function SessionDetail({ tenant, id, onUnauthorized }: { tenant: string; id: string; onUnauthorized: () => void }) {
  const { data, error, loading } = usePoll(
    () => get<SessionResponse>(`/admin/v1/sessions/${encodeURIComponent(id)}`, { tenant }),
    `${tenant}/${id}`,
    onUnauthorized,
  );
  const back = (
    <a className="back" href="#/sessions">
      ← Sessions
    </a>
  );
  if (!data)
    return (
      <>
        {back}
        {error ? <ErrorNotice error={error} /> : <div className="empty">Loading…</div>}
      </>
    );
  const s = data.session;
  const b = s.budget;
  let total = 0;
  const points = data.events.map((e) => {
    total += e.cost_micro_usd;
    const target = e.provider ? `${e.provider}/${e.provider_model}` : 'no target';
    return {
      t: new Date(e.ts).getTime(),
      y: total,
      highlight: Boolean(e.target_switched),
      title: `${time(e.ts)} · ${target}`,
      rows: [
        { name: 'request', value: usd(e.cost_micro_usd) },
        { name: 'session total', value: usd(total) },
        { name: 'cache read tokens', value: count(e.cache_read_tokens) },
        { name: e.target_switched ? 'target switch' : 'status', value: e.target_switched ? `+${usd(e.cache_loss_micro_usd ?? 0)} at most` : String(e.status) },
      ],
    };
  });
  // Show the budget lines only if they fit on a scale that still shows the
  // spend. Else the subtitle gives the budget.
  const allRefs = b
    ? [
        { value: b.limit_micro_usd, label: `Budget ${usd(b.limit_micro_usd)}` },
        { value: b.limit_micro_usd * b.warn_ratio, label: `Warning at ${percent(b.warn_ratio)}` },
      ]
    : [];
  const refs = allRefs.filter((r) => r.value <= Math.max(total, 1) * 3);
  const chartSub =
    b && refs.length < allRefs.length
      ? `The running total of the session. Each dot is one request. The budget of ${usd(b.limit_micro_usd)} is above the chart.`
      : 'The running total of the session. Each dot is one request.';
  return (
    <div className={loading ? 'loading' : undefined}>
      {back}
      <h1 className="page-title" style={{ marginTop: 8 }}>
        {s.session_id}
      </h1>
      <p className="page-lead">
        Tenant {s.tenant_id} · {s.derived ? 'ID derived from the prompt prefix' : 'ID from the x-tollgate-session header'} · {dateTime(s.first)} to{' '}
        {time(s.last)}
      </p>
      {error && <ErrorNotice error={error} />}
      <div className="tiles">
        <Tile hero label="Spend" value={usd(s.cost_micro_usd)} note={`${count(s.requests)} requests`} />
        <Tile
          label="Budget left"
          value={b ? usd(b.remaining_micro_usd) : 'No budget'}
          note={b ? `${percent(b.used_ratio)} of ${usd(b.limit_micro_usd)} used (${b.mode})` : 'The tenant has no session budget.'}
        />
        <Tile
          label="Requests left at the mean cost"
          value={b?.requests_left != null ? count(b.requests_left) : '-'}
          note={b ? `${usd(b.avg_cost_micro_usd)} each; ${minutes(b.minutes_left)} left at ${usd(b.spend_per_minute_micro_usd)}/min` : undefined}
        />
        <Tile label="Prompt read from cache" value={percent(cacheReadShare(s))} note={`${count(s.cache_write_tokens)} cache write tokens`} />
        <Tile label="Target switches" value={count(s.target_switches)} note={`${usd(s.cache_loss_micro_usd)} cache loss at most`} />
      </div>
      <ChartCard
        title="Spend over time"
        sub={chartSub}
        legend={
          <Legend
            series={[
              { name: 'Request', color: 'var(--series-1)' },
              { name: 'Request after a target switch', color: 'var(--series-2)' },
            ]}
          />
        }
        chart={<StepChart points={points} refs={refs} ticks={usdTicks} color="var(--series-1)" highlightColor="var(--series-2)" />}
        table={<EventsTable events={data.events} />}
      />
      <section className="card section-gap">
        <h2>Requests</h2>
        <p className="sub">The most recent 500 requests of the session.</p>
        <div className="table-wrap">
          <EventsTable events={data.events} />
        </div>
      </section>
    </div>
  );
}

function EventsTable({ events }: { events: SessionResponse['events'] }) {
  return (
    <table>
      <thead>
        <tr>
          <th>Time</th>
          <th>Target</th>
          <th className="num">Status</th>
          <th className="num">Attempts</th>
          <th className="num">Input</th>
          <th className="num">Cache read</th>
          <th className="num">Cache write</th>
          <th className="num">Output</th>
          <th className="num">TTFT</th>
          <th className="num">Cost</th>
          <th>Notes</th>
        </tr>
      </thead>
      <tbody>
        {[...events].reverse().map((e) => (
          <tr key={e.request_id}>
            <td>{time(e.ts)}</td>
            <td className="mono">{e.provider ? `${e.provider}/${e.provider_model}` : '-'}</td>
            <td className="num">{e.status}</td>
            <td className="num">{e.attempts}</td>
            <td className="num">{count(e.input_tokens)}</td>
            <td className="num">{count(e.cache_read_tokens)}</td>
            <td className="num">{count(e.cache_write_tokens)}</td>
            <td className="num">{count(e.output_tokens)}</td>
            <td className="num">{ms(e.ttft_ms)}</td>
            <td className="num">{usd(e.cost_micro_usd)}</td>
            <td className="secondary">
              {[
                e.target_switched && `target switch (+${usd(e.cache_loss_micro_usd ?? 0)} at most)`,
                e.budget_cutoff && 'stopped by budget',
                e.cache === 'hit' && 'response cache hit',
                e.redacted && `${e.redacted} ${e.redacted === 1 ? 'value' : 'values'} masked`,
              ]
                .filter(Boolean)
                .join(', ')}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
