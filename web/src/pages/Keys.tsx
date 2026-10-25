import { get, type Tenant } from '../api';
import { ErrorNotice, Meter } from '../components/ui';
import { count, usd } from '../format';
import { usePoll } from '../hooks';

// micro turns a limit_usd value, such as "50.00", into micro-USD.
function micro(limit: string): number {
  return Math.round(Number(limit) * 1e6);
}

export function Keys({ tenant, onUnauthorized }: { tenant: string; onUnauthorized: () => void }) {
  const { data, error, loading } = usePoll(() => get<{ tenants: Tenant[] }>('/admin/v1/keys'), 'keys', onUnauthorized);
  if (!data) return error ? <ErrorNotice error={error} /> : <div className="empty">Loading…</div>;
  const tenants = data.tenants.filter((t) => !tenant || t.id === tenant);
  return (
    <div className={loading ? 'grid loading' : 'grid'}>
      {error && <ErrorNotice error={error} />}
      <p className="page-lead" style={{ margin: 0 }}>
        Keys and limits come from the configuration file. To add a key, run <code>tollgate keys create</code> and reload the configuration. The usage
        is for the current month (UTC).
      </p>
      {tenants.map((t) => (
        <section className="card" key={t.id}>
          <h2>{t.id}</h2>
          <p className="sub">{t.models.join(', ')}</p>
          <div className="grid two">
            <dl className="kv">
              <dt>Requests per minute</dt>
              <dd>{t.limits.requests_per_minute ? count(t.limits.requests_per_minute) : 'no limit'}</dd>
              <dt>Tokens per minute</dt>
              <dd>{t.limits.tokens_per_minute ? count(t.limits.tokens_per_minute) : 'no limit'}</dd>
              <dt>PII redaction</dt>
              <dd>{t.redaction.length > 0 ? t.redaction.join(', ') : 'off'}</dd>
            </dl>
            <dl className="kv">
              {t.budgets.length === 0 && (
                <>
                  <dt>Budgets</dt>
                  <dd>none</dd>
                </>
              )}
              {t.budgets.map((b) => (
                <div key={b.window} style={{ display: 'contents' }}>
                  <dt>
                    {b.window === 'month' ? 'Month budget' : 'Session budget'} ({b.mode})
                  </dt>
                  <dd>
                    {b.spend_micro_usd != null ? (
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                        <Meter ratio={b.spend_micro_usd / micro(b.limit_usd)} label={`${b.window} budget used`} />
                        <span>
                          {usd(b.spend_micro_usd)} of ${b.limit_usd}
                        </span>
                      </div>
                    ) : (
                      <span>${b.limit_usd} for each session</span>
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
          <div className="table-wrap section-gap">
            <table>
              <thead>
                <tr>
                  <th>Key</th>
                  <th className="num">Requests</th>
                  <th className="num">Failed</th>
                  <th className="num">Refused</th>
                  <th className="num">Input tokens</th>
                  <th className="num">Output tokens</th>
                  <th className="num">Spend</th>
                </tr>
              </thead>
              <tbody>
                {t.keys.map((k) => (
                  <tr key={k.id}>
                    <td className="mono">{k.id}</td>
                    <td className="num">{count(k.requests)}</td>
                    <td className="num">{count(k.errors)}</td>
                    <td className="num">{count(k.rejected)}</td>
                    <td className="num">{count(k.input_tokens + k.cache_read_tokens + k.cache_write_tokens)}</td>
                    <td className="num">{count(k.output_tokens)}</td>
                    <td className="num">{usd(k.cost_micro_usd)}</td>
                  </tr>
                ))}
                {t.keys.length === 0 && (
                  <tr>
                    <td colSpan={7} className="empty">
                      The tenant has no keys.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      ))}
    </div>
  );
}
