export const meta = {
  name: 'roadmap-phase',
  description: 'Plan one roadmap phase, then implement it',
  whenToUse: 'Advancing a roadmap one phase at a time. Pass {roadmap, phase, title, sections, ships, control}.',
  phases: [
    { title: 'Plan',      detail: 'read the roadmap section and the decisions, produce a plan' },
    { title: 'Implement', detail: 'build it, tests and docs in the same change' },
  ],
}

const a = args || {}
const ROADMAP = a.roadmap || 'ROADMAP-errors.md'
const PHASE   = a.phase
if (PHASE === undefined) throw new Error('pass {phase: N}')

const CONTEXT = `
Repository: /home/user/ws/shradit/golang/go-rx-crud — the Go CRUD framework "vv"
(module github.com/shardit-io/vv).

Read CLAUDE.md first; it is binding. Then docs/decisions/Index.md — a decision
doc is BINDING, and docs/flows/Index.md's reverse index maps every source file
to the flows that touch it.

The rules broken most often here:
  - A test that would pass if the feature were deleted is a liability. Anything
    that could pass vacuously carries a control case.
  - Docs are updated in the SAME change: the flow that names a moved symbol, the
    reverse index, the row in each directory's Index.md.
  - A use case names no file paths. A flow is the only place they appear.
  - crud/ and the whole root module import the standard library only.
  - Never t.Parallel() in test/integration.
  - Comments say why, never what. Plain, direct, short sentences.

Verify with: make unit · make vet · make check · make integration · gofmt -l .

TASK: ${ROADMAP} phase ${PHASE} — ${a.title || `phase ${PHASE}`}.
${a.sections ? `Governing sections: ${a.sections}` : ''}
${a.ships ? `Ships: ${a.ships}` : ''}
${a.control ? `The control case that must fail without it: ${a.control}` : ''}
`

phase('Plan')
const plan = await agent(`${CONTEXT}

Read the roadmap section, the decisions that bind this phase, and the code it
touches. Produce an implementation plan: the files and what changes in each, the
tests with their control cases, and the docs that must change.

Do not write any code.`, { label: 'plan', phase: 'Plan' })

phase('Implement')
const built = await agent(`${CONTEXT}

Implement this plan. Where it is wrong, fix it and say so.

${plan}

Code, tests and docs in one change. Every test gets its control case. Run
make unit && make vet && make check && gofmt -l . and then make integration.
Do not commit. Report what you did and anything you could not finish.`,
  { label: 'implement', phase: 'Implement' })

return { roadmap: ROADMAP, phase: PHASE, plan, built }
