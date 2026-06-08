# httpcloak docs site

Public docs site for httpcloak, built with [Docusaurus](https://docusaurus.io/).
Deploys to https://httpcloak.dev via Vercel (`Root Directory: docs`).

## Local development

```bash
npm install
npm run start
```

Opens a hot-reloading dev server on http://localhost:3000.

## Production build

```bash
npm run build
```

Static output lands in `build/`. Vercel runs the same command on deploy.
