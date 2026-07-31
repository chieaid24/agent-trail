# Dashboard dependency advisories

The dashboard pins patched direct and transitive packages so `npm audit` reports
no high or critical vulnerabilities.

## Remediation

The dashboard uses Next.js 16.2.12, React 19.2.8, ESLint 9.39.5, and
`eslint-config-next` 16.2.12. These versions satisfy the framework and lint
configuration peer requirements.

The package overrides pin patched transitive releases that the direct packages
do not yet require:

- PostCSS 8.5.25 addresses GHSA-qx2v-qp2m-jg93,
  GHSA-6g55-p6wh-862q, and GHSA-r28c-9q8g-f849.
- Sharp 0.35.3 addresses GHSA-f88m-g3jw-g9cj.
- The local Brace Expansion compatibility package delegates to 5.0.9 to
  address GHSA-mh99-v99m-4gvg while preserving the callable CommonJS export
  required by ESLint's legacy Minimatch consumers.

The Next.js 16 lint configuration also checks render-time ref reads and
synchronous state updates initiated by effects. The dashboard schedules its
initial API and server-sent event synchronization after the effect returns, and
the timeline stores its mount-time animation boundary as immutable state.

Run the audit from the dashboard directory:

```bash
cd apps/web
npm ci
npm audit --audit-level=high
```

## Residual risk

No high or critical advisory remains in the installed dependency graph. Recheck
the overrides when Next.js and the ESLint plugins update their dependency
ranges. Remove the compatibility package once every legacy ESLint consumer no
longer requires the callable Minimatch 3 API.
