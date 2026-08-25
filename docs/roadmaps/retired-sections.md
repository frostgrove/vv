# Retired roadmap sections

`ROADMAP.md`, `ROADMAP-framework.md` and `ROADMAP-errors.md` used to live at the
repository root. They are removed; their history is under `git log --follow`.

Comments in the tree still cite them by section — `ROADMAP-errors.md §2` and
about forty others. This is the map from each old section to its successor, so a
citation can be followed in one hop.

This is reference material, not a plan. What is still open is in
[Roadmap.md](Roadmap.md).

| Old section | Now lives in |
|---|---|
| errors §1 *Why* | [[UC-017]], [[UC-015]] |
| errors §2 *The shape of the answer*, the envelope, the status table | [modules/errs.md](../modules/en/errs.md), [modules/crudhttp.md](../modules/en/crudhttp.md), [[D-049]] |
| errors §3 *The layering*, path translation | [[D-043]], [[D-045]], [[FL-011]] |
| errors §4 *Packages and modules* | [[D-033]], [[D-036]], [[D-048]] |
| errors §5 *The contract — `errs`* | [modules/errs.md](../modules/en/errs.md) |
| errors §6 *Dialect classification* | [modules/sqlerr.md](../modules/en/sqlerr.md), [[D-046]] |
| errors §7 *The catalog* | [modules/catalog.md](../modules/en/catalog.md), [[D-041]], [[FL-016]] |
| errors §8 *The probe* | [modules/probe.md](../modules/en/probe.md), [[D-042]], [[FL-017]] |
| errors §9 *Rendering and the transports* | [modules/crudhttp.md](../modules/en/crudhttp.md), [[FL-011]], [[FL-013]] |
| errors §10 *Codegen* | [modules/vv-cli.md](../modules/en/vv-cli.md), [[D-018]], [[D-050]] |
| errors §11 *Prior art* | [[D-049]], [[D-052]] |
| errors §12 *What binds us* | `docs/usecases/` |
| errors §13 *The hard problems* | §6 and §8 above |
| errors §14 *Phases* | the tree |
| errors §15 *Test strategy* | [[D-020]], `CLAUDE.md` |
| errors §16 *Not decided yet* | §6, §7 and §8 above |
| framework §1–3 *Tiers and the manifest* | [[D-033]], [[D-048]], `make check` |
| framework §4 *Configuration* | [modules/vvflag.md](../modules/en/vvflag.md), [modules/vvcfg.md](../modules/en/vvcfg.md) |
| framework §5 *`app/`* | [[D-037]] |
| framework §6 *Naming* | [[D-035]] |
| framework §7 *The enforcement* | `make check`, and §3 above |
| framework §8–9 *Lockstep, one dependency decision* | [[D-033]], [[D-051]] |
| framework §10 *What this does not become* | [[D-048]] |
| framework §11 *The renames* | §1 above |
| framework §12 *Not decided yet* | §1 above |
