import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'httpcloak',
  tagline: 'A Go HTTP client with browser-grade TLS, HTTP/2, and HTTP/3 fingerprinting',
  favicon: 'img/favicon.ico',

  url: 'https://httpcloak.dev',
  baseUrl: '/',

  organizationName: 'sardanioss',
  projectName: 'httpcloak',

  onBrokenLinks: 'throw',

  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: '/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'httpcloak',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://github.com/sardanioss/httpcloak',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Project',
          items: [
            {
              label: 'GitHub',
              href: 'https://github.com/sardanioss/httpcloak',
            },
            {
              label: 'PyPI',
              href: 'https://pypi.org/project/httpcloak/',
            },
            {
              label: 'npm',
              href: 'https://www.npmjs.com/package/httpcloak',
            },
            {
              label: 'NuGet',
              href: 'https://www.nuget.org/packages/HttpCloak',
            },
          ],
        },
      ],
      copyright: `© ${new Date().getFullYear()} Saksham Solanki. MIT License.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['go', 'csharp', 'bash'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
