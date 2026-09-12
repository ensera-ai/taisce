// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The documentation site's renderer configuration.
//
// The documents are not written here. internal/docsite stages docs/*.md and the generated reference
// into site/docs and writes the navigation to site/sidebars.generated.json; this file only says how
// they are rendered. `make site` does both.
//
// Every broken link, anchor or markdown link fails the build rather than warning: a site that
// publishes with a link to nothing is one a reader stops trusting at the first dead end.

const {themes} = require('prism-react-renderer');

// Source links and the repository button point at the repository and commit the site was built from.
const repository = process.env.SITE_REPO || 'ensera-ai/taisce';
const baseUrl = process.env.SITE_BASE_URL || '/taisce/';

/** @type {import('@docusaurus/types').Config} */
module.exports = {
  title: 'Taisce',
  tagline: 'Open-source memory for AI agents, on PostgreSQL.',
  favicon: 'img/favicon.svg',
  url: 'https://ensera-ai.github.io',
  baseUrl,
  organizationName: 'ensera-ai',
  projectName: 'taisce',
  trailingSlash: false,
  // Read by the landing page, which links contributors to the contributing guide in this repository.
  customFields: {repository},

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  markdown: {
    // .md files are CommonMark, so text such as `{schema}` and `a < b` in a document is text rather
    // than a JSX expression; only .mdx files would be MDX, and there are none.
    format: 'detect',
    mermaid: true,
    hooks: {onBrokenMarkdownLinks: 'throw'},
  },

  i18n: {defaultLocale: 'en', locales: ['en']},

  clientModules: [require.resolve('./src/clientModules/codeTabs.js')],

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          path: 'docs',
          routeBasePath: 'docs',
          sidebarPath: require.resolve('./sidebars.js'),
          // The numbered documents keep their numbers in their addresses, so a link written as
          // 12-recall-controls.md is the page a reader lands on.
          numberPrefixParser: false,
          showLastUpdateTime: false,
        },
        blog: false,
        theme: {customCss: require.resolve('./src/css/custom.css')},
      }),
    ],
  ],

  themes: [
    '@docusaurus/theme-mermaid',
    [
      // Search runs in the reader's browser over an index built with the site. No query leaves it.
      require.resolve('@easyops-cn/docusaurus-search-local'),
      {
        hashed: true,
        docsRouteBasePath: 'docs',
        indexBlog: false,
        indexPages: false,
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
        searchResultLimits: 12,
        searchContextByPaths: [
          {label: 'Code reference', path: 'docs/reference/code'},
          {label: 'Schema', path: 'docs/reference/schema'},
          {label: 'Migrations', path: 'docs/reference/migrations'},
        ],
        useAllContextsWithNoSearchContext: true,
      },
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      colorMode: {defaultMode: 'dark', respectPrefersColorScheme: false},
      image: 'img/favicon.svg',
      navbar: {
        title: 'taisce.',
        hideOnScroll: false,
        items: [
          {to: '/docs/developers/quickstart', position: 'left', label: 'Quickstart'},
          {to: '/docs/examples/overview', position: 'left', label: 'Examples'},
          {type: 'docSidebar', sidebarId: 'guide', position: 'left', label: 'Docs'},
          {type: 'docSidebar', sidebarId: 'reference', position: 'left', label: 'Reference'},
          {href: `https://github.com/${repository}`, position: 'right', label: 'GitHub'},
          {href: 'https://ensera.ai', position: 'right', label: 'Ensera', className: 'navbar-ensera'},
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Start',
            items: [
              {label: 'How it works', to: '/docs/start/how-it-works'},
              {label: 'Quickstart', to: '/docs/developers/quickstart'},
              {label: 'Examples', to: '/docs/examples/overview'},
            ],
          },
          {
            title: 'Build with it',
            items: [
              {label: 'Python', to: '/docs/developers/python'},
              {label: 'Java', to: '/docs/developers/java'},
              {label: '.NET', to: '/docs/developers/dotnet'},
              {label: 'HTTP API', to: '/docs/developers/http-api'},
              {label: 'MCP', to: '/docs/developers/mcp'},
            ],
          },
          {
            title: 'Go deeper',
            items: [
              {label: 'Architecture', to: '/docs/architecture/overview'},
              {label: 'PostgreSQL', to: '/docs/postgresql/overview'},
              {label: 'Code reference', to: '/docs/reference/code/overview'},
            ],
          },
          {
            title: 'Project',
            items: [
              {label: 'GitHub', href: `https://github.com/${repository}`},
              {label: 'Ensera', href: 'https://ensera.ai'},
              {label: 'Contributors', to: '/#contributors'},
            ],
          },
        ],
        copyright:
          '© 2026 The Taisce Authors · Apache-2.0 · Built by <a href="https://ensera.ai">Ensera</a> and ' +
          `<a href="${baseUrl}#contributors">its contributors</a>`,
      },
      prism: {
        theme: themes.github,
        darkTheme: themes.oneDark,
        additionalLanguages: ['bash', 'go', 'sql', 'json', 'java', 'csharp', 'python', 'yaml', 'toml', 'diff', 'http', 'kotlin', 'groovy'],
      },
      mermaid: {
        theme: {light: 'base', dark: 'base'},
        options: {
          fontFamily: 'Manrope, system-ui, sans-serif',
          themeVariables: {
            primaryColor: '#0f2c36',
            primaryTextColor: '#e8f1f2',
            primaryBorderColor: '#8fe3c0',
            lineColor: '#8fe3c0',
            secondaryColor: '#12303a',
            tertiaryColor: '#071821',
            actorBkg: '#0f2c36',
            actorBorder: '#5fb8ff',
            actorTextColor: '#e8f1f2',
            signalColor: '#d6f26b',
            signalTextColor: '#e8f1f2',
            noteBkgColor: '#16343d',
            noteTextColor: '#e8f1f2',
            edgeLabelBackground: '#071821',
          },
        },
      },
      tableOfContents: {minHeadingLevel: 2, maxHeadingLevel: 3},
    }),
};
