import { get, type UsageResponse } from '../api';
import { ChartCard, ColumnChart, Legend, usdTicks, type Series } from '../components/charts';
import { ErrorNotice, Tile } from '../components/ui';
import { cacheReadShare, count, percent, usd } from '../format';
import { bucketLabel, buckets, usePoll, type Range } from '../hooks';

interface Data {
  total: UsageResponse;
  series: UsageResponse;
  routes: UsageResponse;
}

const requestSeries: Series[] = [
  { name: 'Successful', color: 'var(--series-1)' },
  { name: 'Refused (4xx)', color: 'var(--warning)' },
  { name: 'Failed (5xx)', color: 'var(--critical)' },
];

export function Overview({ range, tenant, onUnauthorized }: { range: Range; tenant: string; onUnauthorized: () => void }) {
  const params = { from: range.from.toISOString(), to: range.to.toISOString(), tenant };
  const { data, error, loading } = usePoll<Data>(
    async () => {
      const [total, series, routes] = await Promise.all([
        get<UsageResponse>('/admin/v1/usage', params),
        get<UsageResponse>('/admin/v1/usage', { ...params, group_by: range.bucket }),
        get<UsageResponse>('/admin/v1/usage', { ...params, group_by: 'route' }),
      ]);
      return { total, series, routes };
    },
    `${range.name}:${range.from.getTime()}:${tenant}`,
    onUnauthorized,
  );
  if (!data) return error ? <ErrorNotice error={error} /> : <div className="empty">Loading…</div>;

  const t = data.total.groups[0];
  const byKey = new Map(data.series.groups.map((g) => [g.key, g]));
  const keys = buckets(range);
  const spendCols = keys.map((k) => ({ key: k, label: bucketLabel(k, range.bucket, range.name), values: [byKey.get(k)?.cost_micro_usd ?? 0] }));
  const reqCols = keys.map((k) => {
    const g = byKey.get(k);
    return {
      key: k,
      label: bucketLabel(k, range.bucket, range.name),
      values: g ? [g.requests - g.rejected - g.errors, g.rejected, g.errors] : [0, 0, 0],
    };
  });
  const per = range.bucket === 'hour' ? 'hour' : 'day';

  return (
    <div className={loading ? 'loading' : undefined}>
      {error && <ErrorNotice error={error} />}
      <div className="tiles">
        <Tile hero label="Spend" value={usd(t?.cost_micro_usd ?? 0)} note={`${count(t?.requests ?? 0)} ${t?.requests === 1 ? 'request' : 'requests'}`} />
        <Tile
          label="Failed requests"
          value={t && t.requests > 0 ? percent(t.errors / t.requests, 1) : '0%'}
          note={`${count(t?.errors ?? 0)} failed, ${count(t?.rejected ?? 0)} refused`}
        />
        <Tile
          label="Prompt read from cache"
          value={t ? percent(cacheReadShare(t)) : '-'}
          note={`${count(t?.cache_read_tokens ?? 0)} cache read tokens`}
        />
        <Tile label="Requests with a fallback" value={count(t?.fallbacks ?? 0)} note={`${count(t?.target_switches ?? 0)} target switches in sessions`} />
        <Tile label="Cache loss (upper bound)" value={usd(t?.cache_loss_micro_usd ?? 0)} note="Extra cost after a target switch" />
        <Tile label="Response cache hits" value={count(t?.cache_hits ?? 0)} note={`${count(t?.budget_cutoffs ?? 0)} budget cutoffs`} />
      </div>
      <div className="grid two">
        <ChartCard
          title={`Spend per ${per}`}
          sub="Cost of the requests from the price table, in USD."
          chart={<ColumnChart columns={spendCols} series={[{ name: 'Spend', color: 'var(--series-1)' }]} format={usd} ticks={usdTicks} />}
          table={
            <table>
              <thead>
                <tr>
                  <th>Time</th>
                  <th className="num">Spend</th>
                </tr>
              </thead>
              <tbody>
                {spendCols.map((c) => (
                  <tr key={c.key}>
                    <td>{c.label}</td>
                    <td className="num">{usd(c.values[0] ?? 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          }
        />
        <ChartCard
          title={`Requests per ${per}`}
          sub="Refused requests got a 4xx status from Tollgate or the provider. Failed requests got a 5xx status."
          legend={<Legend series={requestSeries} />}
          chart={<ColumnChart columns={reqCols} series={requestSeries} format={(v) => count(Math.round(v))} minStep={1} />}
          table={
            <table>
              <thead>
                <tr>
                  <th>Time</th>
                  {requestSeries.map((s) => (
                    <th className="num" key={s.name}>
                      {s.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {reqCols.map((c) => (
                  <tr key={c.key}>
                    <td>{c.label}</td>
                    {c.values.map((v, i) => (
                      <td className="num" key={i}>
                        {count(v)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          }
        />
      </div>
      <section className="card section-gap">
        <h2>Routes</h2>
        <p className="sub">The model names that clients send, by spend.</p>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Route</th>
                <th className="num">Requests</th>
                <th className="num">Failed</th>
                <th className="num">Input tokens</th>
                <th className="num">Cache read</th>
                <th className="num">Cache write</th>
                <th className="num">Output tokens</th>
                <th className="num">Spend</th>
              </tr>
            </thead>
            <tbody>
              {data.routes.groups.map((g) => (
                <tr key={g.key}>
                  <td className="mono">{g.key || '-'}</td>
                  <td className="num">{count(g.requests)}</td>
                  <td className="num">{count(g.errors)}</td>
                  <td className="num">{count(g.input_tokens)}</td>
                  <td className="num">{count(g.cache_read_tokens)}</td>
                  <td className="num">{count(g.cache_write_tokens)}</td>
                  <td className="num">{count(g.output_tokens)}</td>
                  <td className="num">{usd(g.cost_micro_usd)}</td>
                </tr>
              ))}
              {data.routes.groups.length === 0 && (
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
