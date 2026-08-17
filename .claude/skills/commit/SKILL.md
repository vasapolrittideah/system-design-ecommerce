---
name: commit
description: Create git commits that follow the Conventional Commits spec. Use when the user asks to commit, stage and commit, "commit this", "cm", or wants staged/unstaged changes turned into one or more well-formed commits. Also use when reviewing or rewriting an existing commit message for conventional-commit compliance.
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Bash(git add:*), Bash(git commit:*), Bash(git rev-parse:*), Bash(git branch:*), Read, Grep, Glob
---

# Conventional Commit

Turn working-tree changes into one or more commits that follow
[Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).

## Message format

```
<type>(<optional scope>)<optional !>: <description>

<body>

<footer>
```

- **type** — required, lowercase, from the table below.
- **scope** — optional but strongly preferred in this repo. See "Scopes".
- **`!`** — append before the colon for a breaking change.
- **description** — required. Imperative mood ("add", not "added"/"adds"),
  lowercase first letter, no trailing period, **English**, ≤ 72 chars total
  header length. See "Articles in the description".
- **body** — optional. Wrap at 72 cols. Explain **why**, not what — the diff
  already says what. Blank line before it.
- **footer** — optional. `BREAKING CHANGE: <desc>`, `Refs: #123`, `Closes: #123`.

### Articles in the description

**Drop `a`, `an`, and `the`.** The description is telegraphic — the register of a
news headline, where articles are the first thing cut. The spec says nothing
about them; this is house style. The type prefix already carries what an article
might: `feat` says the thing is new, `fix` and `refactor` say it was already
there. So the article adds no information and spends part of the 72-char budget.

| Don't | Do |
|---|---|
| `fix(identity): handle the null response from the auth API` | `fix(identity): handle null response from auth API` |
| `feat(payment): add a retry to the payment webhook` | `feat(payment): add retry to payment webhook` |
| `refactor(order): extract the validation into a helper` | `refactor(order): extract validation into helper` |

**Stop where the cut costs meaning.** Terseness is the point only while the line
still reads cleanly; an article that removes an ambiguity has earned its
characters. Keep one:

- where dropping it is ungrammatical rather than merely terse — before a noun
  that is the subject of a verb (`record what the domain import rule protects`)
  or before an ordinal (`take context as the first parameter`)
- where the line parses without it but stumbles on the way through —
  `fix(cart): prevent panic when the user has no active session` over `when user
  has no active session`

Where both read fine, drop it.

Check the mood separately: "If applied, this commit will `<description>`" must
read as a grammatical sentence.

## Types

| Type | Use for |
|---|---|
| `feat` | new user-facing capability |
| `fix` | bug fix |
| `docs` | documentation only |
| `style` | formatting, whitespace, `gofmt` — no behaviour change |
| `refactor` | restructuring with no behaviour or API change |
| `perf` | performance improvement |
| `test` | adding or fixing tests |
| `build` | build system, Dockerfile, Makefile, go.mod/go.sum |
| `ci` | CI config and workflows |
| `chore` | maintenance that fits nothing else (deps bumps, .gitignore) |
| `revert` | reverting a previous commit |

Pick by **intent**, not by file path. A change to a `_test.go` file that fixes a
real bug in the test itself is `fix`; adding coverage for existing code is `test`.

## Scopes

Derive the scope from the paths touched:

| Paths | Scope |
|---|---|
| `services/<name>/**` | the service name, e.g. `order`, `catalog`, `payment` |
| `pkg/<name>/**` | the package name, e.g. `logger`, `outbox`, `otel` |
| `api/proto/**` | `proto` |
| `deploy/**`, `k8s/**` | `deploy` |
| `.github/workflows/**` | `ci` |
| `CLAUDE.md`, `.claude/**` | `agent` — see "Agent config" |
| repo root config, `Makefile`, tooling | omit the scope |

Rules:
- One scope per commit. If a change genuinely spans services, that is a signal to
  split it — see "Splitting".
- Lowercase, single word where possible. No slashes.

## Agent config

Files that configure Claude Code — `CLAUDE.md`, `.claude/skills/**`,
`.claude/agents/**`, `.claude/settings.json`, hooks — all use scope `agent`.

Pick the type by what kind of file it is:

| File | Type |
|---|---|
| `CLAUDE.md` prose | `docs` |
| skills, agents, settings, hooks | `chore` |
| correcting something wrong in the above | `fix` |

Never use `feat` for agent config. These files do not change the deployed
artifact, so they must not trigger a release bump.

```
docs(agent): document outbox pattern convention
chore(agent): add conventional-commit skill
chore(agent): allow go test in project permissions
fix(agent): correct scope table paths in commit skill
```

## Breaking changes

A breaking change is anything that forces a consumer to change: removing or
renaming a proto field or RPC, changing an HTTP response shape, an event schema
change that old consumers cannot read, or a config key rename.

Mark it **both** ways:

```
feat(proto)!: remove deprecated OrderStatus.PENDING

BREAKING CHANGE: OrderStatus.PENDING is gone. Consumers must handle
CREATED instead. Bump order/v1 consumers before deploying.
```

## Workflow

1. **Look before staging.** Run in parallel:
   - `git status --porcelain`
   - `git diff` (unstaged)
   - `git diff --staged`
   - `git log --oneline -10` — match the repo's existing style and scope names.
2. **If something is already staged**, commit only what is staged unless the user
   asked for everything. Do not silently widen the commit.
3. **Group the changes** into logical commits (see "Splitting").
4. **Stage explicitly** — `git add <path> ...` with named paths. Never
   `git add -A` or `git add .`; they pick up files the user did not intend.
5. **Commit** with a heredoc so the body formats correctly:

   ```bash
   git commit -F - <<'EOF'
   feat(order): add saga orchestrator for checkout

   Sequences inventory reservation and payment capture with
   compensating actions so a failed charge releases held stock.

   Refs: #42
   EOF
   ```
6. **Verify** with `git log -1 --stat` and report the resulting commit(s) to the
   user, one line each.

## Splitting

Prefer several small commits over one large one. Split when the change mixes:

- different types (a `fix` bundled with a `refactor`)
- different scopes (`order` and `catalog`)
- generated code and hand-written code (commit `sqlc`/`buf` output separately)
- a dependency bump and the code that uses it

Order commits so the repo builds at every step: proto/contract first, then
producers, then consumers.

When a split would need partial staging of a single file, do **not** try to be
clever with `git add -p` — tell the user which file needs manual splitting and
commit the rest.

## Hard rules

- **Never commit unless the user asked for it.** This skill runs on request only.
- **Never push.** Committing and pushing are separate asks.
- **Never `--amend`, rebase, reset, or force** anything without an explicit
  instruction naming that operation.
- **Never `--no-verify`.** If a hook fails, fix the cause. If the fix is not
  obvious, stop and report the hook output.
- If a pre-commit hook modifies files, re-stage them and retry the commit once.
  If it fails again, stop and report.
- **Commit straight to `trunk`.** This repo practises trunk-based development:
  no topic branch and no PR for ordinary work. Never create one unless the
  user explicitly asks for it.
- **Trunk stays releasable.** Never land a commit that leaves the build or the
  tests broken. If a change is too large to land safely in one commit, split it
  (see "Splitting") or put it behind a flag — never open a long-lived branch.
- **Never commit secrets.** Before staging, scan the diff for `.env` files,
  private keys, `AWS_`/`SECRET`/`TOKEN`/`PASSWORD` literals. If found, stop and
  ask.
- End the message with the trailer required by the harness:
  `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`
  (drop this line if the user says they don't want it.)

## Examples

```
feat(catalog): add full-text product search via Elasticsearch
fix(payment): make Stripe charge idempotent on retry

The gateway retried on a 504 and double-charged. Pass the order ID as
the Stripe idempotency key so retries collapse to one charge.

Closes: #118
```

```
refactor(order): move saga state machine into domain layer
test(inventory): cover concurrent stock reservation with testcontainers
build(deps): bump pgx to v5.6
ci: run integration tests only for changed services
docs(proto): document event envelope fields
chore: ignore local docker-compose override
```

Bad → good:

| Bad | Why | Good |
|---|---|---|
| `update code` | no type, meaningless | `refactor(cart): extract Redis key builder` |
| `Fixed the bug.` | past tense, capital, period, no scope | `fix(order): reject checkout with empty cart` |
| `feat: stuff for order and catalog and proto` | multi-scope grab bag | split into three commits |
| `feat(order): changed status field name` | breaking, unmarked | `feat(order)!: rename status to state` + `BREAKING CHANGE:` footer |
