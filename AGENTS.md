# AGENTS.md

## v40 wiring

Before bootstrapping a checkout, running the test suite, starting a dev server,
or waiting on CI in this repository, read [`.v40/wiring.md`](.v40/wiring.md).

It records the bootstrap and suite commands and why the suite runs with `-short`,
the verified fact that this repository has no CI, the machine-global resources a
parallel run can collide on with the knob that isolates each and the probe that
proves ownership, and the two build targets (`bindings/clib`, `docs/`) the suite
does not cover.

The machine-readable knobs live beside it in [`.v40/env`](.v40/env), which
`shipctl.sh` sources for itself. An exported environment variable overrides that
file; that file overrides shipctl's built-in defaults.
