import {copyFileSync, mkdirSync, readdirSync, readFileSync} from 'node:fs';
import {dirname, join, posix} from 'node:path';
import {fileURLToPath} from 'node:url';
import {defineConfig, type DefaultTheme} from 'vitepress';

// The documentation site. Run "make docs-serve" to edit it, and "make docs" to build it.
// GitHub Pages serves the build at https://ykakade.github.io/tollgate/.

const repo = 'https://github.com/ykakade/tollgate';
const docsDir = join(dirname(fileURLToPath(import.meta.url)), '..');

// One sidebar entry for each ADR, with the title from its first line.
const adrs: DefaultTheme.SidebarItem[] = readdirSync(join(docsDir, 'adr'))
  .filter((f) => /^\d{4}-.*\.md$/.test(f))
  .sort()
  .map((f) => {
    const title = readFileSync(join(docsDir, 'adr', f), 'utf8').split('\n')[0].replace(/^#\s*/, '');
    return {text: title.replace(/^ADR (\d+): /, '$1 · '), link: `/adr/${f.replace(/\.md$/, '')}`};
  });

export default defineConfig({
  title: 'Tollgate',
  description: 'A lightweight AI gateway for coding agents. One Go binary that controls cost, rate limits and failover for the Anthropic and OpenAI APIs.',
  base: '/tollgate/',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  // Markdown download links retain their source names. Video and image
  // embeds are bundled separately by Vite with hashed names.
  buildEnd({outDir}) {
    mkdirSync(join(outDir, 'assets'), {recursive: true});
    for (const file of ['tollgate.mp4', 'tollgate.webp']) {
      copyFileSync(join(docsDir, 'assets', file), join(outDir, 'assets', file));
    }
  },
  srcExclude: ['**/node_modules/**', 'video/out/**', 'releases/**', 'README.md'],
  // The README pages of directories are their index pages on the site.
  rewrites: {
    'adr/README.md': 'adr/index.md',
    'demo/README.md': 'demo/index.md',
    'video/README.md': 'video/index.md',
  },
  // Addresses such as http://127.0.0.1:9090 are local examples, not links to check.
  ignoreDeadLinks: 'localhostLinks',
  head: [
    ['link', {rel: 'icon', type: 'image/svg+xml', href: '/tollgate/favicon.svg'}],
    ['meta', {name: 'theme-color', content: '#2a78d6'}],
    ['meta', {property: 'og:title', content: 'Tollgate'}],
    ['meta', {property: 'og:description', content: 'A lightweight AI gateway for coding agents.'}],
  ],
  markdown: {
    config: (md) => {
      // The docs link to source files with paths such as ../internal/server.
      // Those files are not part of the site, so link to them on GitHub.
      const rule = md.renderer.rules.link_open;
      md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
        const href = tokens[idx].attrGet('href');
        if (href && !/^[a-z]+:|^#|^\//i.test(href)) {
          const [path, hash] = href.split('#');
          const target = posix.normalize(posix.join(posix.dirname(env.relativePath ?? ''), path));
          if (target.startsWith('../')) {
            const file = posix.normalize(posix.join('docs', target));
            const kind = file.endsWith('/') || !/\.[a-z0-9]+$/i.test(file) ? 'tree' : 'blob';
            tokens[idx].attrSet('href', `${repo}/${kind}/main/${file}${hash ? `#${hash}` : ''}`);
          }
        }
        return rule ? rule(tokens, idx, options, env, self) : self.renderToken(tokens, idx, options);
      };
    },
  },
  themeConfig: {
    logo: '/favicon.svg',
    nav: [
      {text: 'Quick start', link: '/quickstart'},
      {text: 'Guides', link: '/configuration'},
      {text: 'Decisions', link: '/adr/'},
      {
        text: 'v0.1.0',
        items: [
          {text: 'Release notes', link: `${repo}/releases/tag/v0.1.0`},
          {text: 'Changelog', link: `${repo}/blob/main/CHANGELOG.md`},
          {text: 'Contributing', link: `${repo}/blob/main/CONTRIBUTING.md`},
        ],
      },
    ],
    sidebar: [
      {
        text: 'Start',
        items: [
          {text: 'Overview', link: '/'},
          {text: 'Quick start', link: '/quickstart'},
        ],
      },
      {
        text: 'Guides',
        items: [
          {text: 'Configuration', link: '/configuration'},
          {text: 'Rate limits and budgets', link: '/limits'},
          {text: 'Reliability', link: '/reliability'},
          {text: 'Agent features', link: '/agent-features'},
          {text: 'Admin API and dashboard', link: '/admin-api'},
          {text: 'Observability', link: '/observability'},
          {text: 'Operations', link: '/operations'},
        ],
      },
      {
        text: 'Reference',
        items: [
          {text: 'Glossary', link: '/glossary'},
          {text: 'M1 validation', link: '/m1-validation'},
          {text: 'Demo recording', link: '/demo/'},
          {text: 'Launch video', link: '/video/'},
        ],
      },
      {
        text: 'Decision records',
        collapsed: true,
        items: [{text: 'Index', link: '/adr/'}, ...adrs],
      },
    ],
    socialLinks: [{icon: 'github', link: repo}],
    editLink: {pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub'},
    search: {provider: 'local'},
    outline: {level: [2, 3]},
    footer: {message: 'Released under the Apache-2.0 license.'},
  },
});
