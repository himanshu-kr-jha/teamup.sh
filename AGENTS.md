## Agent skills

### Issue tracker

Issues live as markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: CONTEXT.md + docs/adr/ at repo root. See `docs/agents/domain.md`.

### Building, testing and deploying

`docs/ONBOARDING.md` is the developer flow and `docs/BUILD-AND-DEPLOY.md` the release flow.
`make check` is what a change must pass; `make deploy` ships a commit to the server named in the untracked `.deploy.env`.
