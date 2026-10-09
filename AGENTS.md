- This repository is the `rinspacehq/gitea` fork of `go-gitea/gitea`. Develop
  Rinspace changes on the current `rinspace-<major>.<minor>` maintenance line.
- Verify `git remote -v` before network operations. `origin` must be
  `rinspacehq/gitea`; the official Gitea repository is fetch-only `upstream`.
- Preserve the upstream MIT License, authorship, tags, and history. Rinspace
  changes in this fork are MIT licensed as well.
- Keep Rinspace integration disabled unless its documented environment is
  explicitly configured. Never commit production endpoints, credentials,
  databases, repository data, or deployment configuration.
- Release only from an immutable reviewed commit through the Rinspace release
  workflow. Consumers must pin the resulting OCI digest, never a branch or
  mutable tag.
- Do not enable the upstream Gitea release, nightly, Snapcraft, bot, or
  translation workflows for this fork.
- Use `make help` to find available development targets
- Run `make fmt` to format `.go` files, and run `make lint-go` to lint them
- Run `make lint-js` to lint `.ts` files
- Run `make tidy` after any `go.mod` changes
- Run single go tests with `go test -run '^TestName$' ./modulepath/`
- Run single js test files with `pnpm exec vitest <path-filter>`
- Run single playwright e2e test files with `GITEA_TEST_E2E_FLAGS='<filepath>' make test-e2e`
- Add the current year into the copyright header of new `.go` files
- Ensure no trailing whitespace in edited files
- Use Conventional Commits for commit messages and PR titles, e.g. `type(scope): subject`; `!` before the colon if breaking. Use `test` type for test-only changes.
- Never force-push, amend, or squash unless asked. Use new commits and normal push for pull request updates
- Preserve existing code comments, do not remove or rewrite comments that are still relevant
- Keep comments short, prefer same-line, explain why, never narrate code
- Prefer unit tests over integration tests when logic is testable in isolation
- Aim for sub-2s local runtime for integration and e2e tests
- In TypeScript, use `!` (non-null assertion) instead of `?.`/`??` when a value is known to always exist
- For CSS layout, prefer `flex-*` helpers over per-child `tw-ml-*` / `tw-mr-*` margins; fall back to `tw-*` utilities when specificity requires `!important`
- Include authorship attribution in issue and pull request comments
- Always add `Assisted-By` trailers to commit messages in format `Assisted-by: AGENT_NAME:MODEL_VERSION`
- Never add `Co-Authored-By` `Signed-off-by` trailer to commit messages. Sign off must be done by a human.
