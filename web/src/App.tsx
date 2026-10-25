import { useCallback, useMemo, useState } from 'react';

import { get, getToken, setToken, type Info, type Tenant } from './api';
import { ago } from './format';
import { makeRange, useHash, usePoll, useTheme, type RangeName, type Theme } from './hooks';
import { Keys } from './pages/Keys';
import { Overview } from './pages/Overview';
import { Providers } from './pages/Providers';
import { SessionDetail, Sessions } from './pages/Sessions';

const pages = [
  { path: '/', name: 'Overview' },
  { path: '/sessions', name: 'Sessions' },
  { path: '/providers', name: 'Providers' },
  { path: '/keys', name: 'Keys' },
];

const ranges: { name: RangeName; label: string }[] = [
  { name: '24h', label: 'Last 24 hours' },
  { name: '7d', label: 'Last 7 days' },
  { name: '30d', label: 'Last 30 days' },
];

const themes: { name: Theme; label: string }[] = [
  { name: 'system', label: 'System' },
  { name: 'light', label: 'Light' },
  { name: 'dark', label: 'Dark' },
];

function Logo() {
  return (
    <svg width="22" height="22" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--series-1)" />
      <path d="M7 12h18M16 12v13" stroke="#fff" strokeWidth="3.5" strokeLinecap="round" />
    </svg>
  );
}

function TokenForm({ onSave }: { onSave: () => void }) {
  const [value, setValue] = useState(getToken());
  return (
    <div className="notice" role="alert">
      <strong>The admin API needs a token.</strong> The admin port of this Tollgate is not on a loopback address, so each API request needs the
      value of <code>server.admin_token</code>. The dashboard keeps the token in this browser tab only.
      <form
        className="token-form"
        onSubmit={(e) => {
          e.preventDefault();
          setToken(value.trim());
          onSave();
        }}
      >
        <input type="password" aria-label="Admin token" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
        <button className="icon-button" type="submit">
          Save
        </button>
      </form>
    </div>
  );
}

export function App() {
  const hash = useHash();
  const [theme, setTheme] = useTheme();
  const [rangeName, setRangeName] = useState<RangeName>('24h');
  const [tenant, setTenant] = useState('');
  const [needToken, setNeedToken] = useState(false);
  const [generation, setGeneration] = useState(0);
  const onUnauthorized = useCallback(() => setNeedToken(true), []);
  // A new range object each minute moves the window forward.
  const minute = Math.floor(Date.now() / 60_000);
  const range = useMemo(() => makeRange(rangeName), [rangeName, minute, generation]);
  const info = usePoll(() => get<Info>('/admin/v1/info'), `info:${generation}`, onUnauthorized);
  const tenants = usePoll(() => get<{ tenants: Tenant[] }>('/admin/v1/keys'), `tenants:${generation}`, onUnauthorized);

  const sessionMatch = /^\/sessions\/([^/]+)\/(.+)$/.exec(hash);
  const section = sessionMatch ? '/sessions' : (pages.find((p) => p.path === hash)?.path ?? '/');
  const common = { range, tenant, onUnauthorized };

  let page;
  if (needToken) {
    page = (
      <TokenForm
        onSave={() => {
          setNeedToken(false);
          setGeneration((g) => g + 1);
        }}
      />
    );
  } else if (sessionMatch) {
    page = <SessionDetail key={hash} tenant={decodeURIComponent(sessionMatch[1]!)} id={decodeURIComponent(sessionMatch[2]!)} onUnauthorized={onUnauthorized} />;
  } else if (section === '/sessions') {
    page = <Sessions key={generation} {...common} />;
  } else if (section === '/providers') {
    page = <Providers key={generation} {...common} />;
  } else if (section === '/keys') {
    page = <Keys key={generation} tenant={tenant} onUnauthorized={onUnauthorized} />;
  } else {
    page = <Overview key={generation} {...common} />;
  }

  return (
    <div className="app">
      <header className="topbar">
        <a className="brand" href="#/">
          <Logo />
          Tollgate
        </a>
        <nav className="nav" aria-label="Pages">
          {pages.map((p) => (
            <a key={p.path} href={`#${p.path}`} aria-current={section === p.path ? 'page' : undefined}>
              {p.name}
            </a>
          ))}
        </nav>
        <span className="spacer" />
        {info.data && (
          <span className="meta">
            {info.data.version} · {info.data.store} store · started {ago(info.data.started)} · {info.data.events.toLocaleString()} events in memory
          </span>
        )}
        <div className="segmented" role="group" aria-label="Theme">
          {themes.map((t) => (
            <button key={t.name} aria-pressed={theme === t.name} onClick={() => setTheme(t.name)}>
              {t.label}
            </button>
          ))}
        </div>
      </header>
      {!sessionMatch && section !== '/keys' && (
        <div className="filters">
          <div className="segmented" role="group" aria-label="Time range">
            {ranges.map((r) => (
              <button key={r.name} aria-pressed={rangeName === r.name} onClick={() => setRangeName(r.name)}>
                {r.label}
              </button>
            ))}
          </div>
          <label>
            Tenant
            <select value={tenant} onChange={(e) => setTenant(e.target.value)}>
              <option value="">All tenants</option>
              {tenants.data?.tenants.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.id}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      <main>{page}</main>
    </div>
  );
}
