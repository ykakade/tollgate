import { get, type Breaker, type UsageResponse } from '../api';
import { ChartCard, RangeChart } from '../components/charts';
import { BreakerBadge, ErrorNotice } from '../components/ui';
import { cacheReadShare, count, ms, percent, usd } from '../format';
import { usePoll, type Range } from '../hooks';

interface Data {
  breakers: Breaker[];
  usage: UsageResponse;
}

export function Providers({ range, tenant, onUnauthorized }: { range: Range; tenant: string; onUnauthorized: () => void }) {
  const { data, error, loading } = usePoll<Data>(
    async () => {
      const [b, usage] = await Promise.all([
        get<{ breakers: Breaker[] | null }>('/admin/v1/breakers'),
        get<UsageResponse>('/admin/v1/usage', { from: range.from.toISOString(), to: range.to.toISOString(), tenant, group_by: 'provider' }),
      ]);
      return { breakers: b.breakers ?? [], usage };
    },
    `${range.name}:${range.from.getTime()}:${tenant}`,
    onUnauthorized,
  );
  if (!data) return error ? <ErrorNotice error={error} /> : <div className="empty">Loading…</div>;
  const targets = data.usage.groups.filter((g) => g.key !== '');
  const ttft = targets
    .filter((g) => g.ttft_p50_ms != null && g.ttft_p95_ms != null)
    .map((g) => ({ label: g.key, low: g.ttft_p50_ms!, high: g.ttft_p95_ms! }));
  return (
    <div className={loading ? 'loading' : undefined}>
      {error && <ErrorNotice error={error} />}
      <div className="grid two">
        <section className="card">
          <h2>Circuit breakers</h2>
          <p className="sub">One breaker for each provider, model and region. An open breaker refuses attempts for breaker.open_for.</p>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Model</th>
                  <th>Region</th>
                  <th>State</th>
                </tr>
              </thead>
              <tbody>
                {data.breakers.map((b) => (
                  <tr key={`${b.provider}/${b.model}/${b.region ?? ''}`}>
                    <td>{b.provider}</td>
                    <td className="mono">{b.model}</td>
                    <td className="secondary">{b.region || '-'}</td>
                    <td>
                      <BreakerBadge state={b.state} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        <ChartCard
          title="Time to first token"
          sub="The dot is the median (p50). The line ends at the 95th percentile (p95)."
          chart={
            ttft.length > 0 ? (
              <RangeChart rows={ttft} format={(v) => ms(Math.round(v))} color="var(--series-1)" lowName="p50" highName="p95" />
            ) : (
              <div className="empty">No responses in this time range.</div>
            )
          }
          table={
            <table>
              <thead>
                <tr>
                  <th>Target</th>
                  <th className="num">p50</th>
                  <th className="num">p95</th>
                </tr>
              </thead>
              <tbody>
                {ttft.map((r) => (
                  <tr key={r.label}>
                    <td className="mono">{r.label}</td>
                    <td className="num">{ms(r.low)}</td>
                    <td className="num">{ms(r.high)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          }
        />
      </div>
      <section className="card section-gap">
        <h2>Targets</h2>
        <p className="sub">The target of the last attempt of each request.</p>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Target</th>
                <th className="num">Requests</th>
                <th className="num">Failed</th>
                <th className="num">With fallback</th>
                <th className="num">Prompt from cache</th>
                <th className="num">TTFT p50</th>
                <th className="num">TTFT p95</th>
                <th className="num">Spend</th>
              </tr>
            </thead>
            <tbody>
              {targets.map((g) => (
                <tr key={g.key}>
                  <td className="mono">{g.key}</td>
                  <td className="num">{count(g.requests)}</td>
                  <td className="num">{count(g.errors)}</td>
                  <td className="num">{count(g.fallbacks)}</td>
                  <td className="num">{percent(cacheReadShare(g))}</td>
                  <td className="num">{ms(g.ttft_p50_ms)}</td>
                  <td className="num">{ms(g.ttft_p95_ms)}</td>
                  <td className="num">{usd(g.cost_micro_usd)}</td>
                </tr>
              ))}
              {targets.length === 0 && (
                <tr>
                  <td colSpan={8} className="empty">
                    No requests in this time range.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
