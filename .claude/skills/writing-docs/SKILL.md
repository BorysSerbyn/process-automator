---
name: writing-docs
description: How process-automator's docs are organised and formatted. Use when adding to or editing anything in docs/ (requirements, technical decisions, scope, guides).
---

# Writing docs

The docs are an Obsidian vault in the repo: `docs/`.

## Layout
- `Home.md`: the index; link every new page from it
- `Spec/`: the one current spec of the app
  - `Scope.md`: what the app is and isn't, in a page
  - `Requirements/Functional/`: what it does (`Functional Requirements.md`, `Agentic Functional Requirements.md` for what LLM clients can do through it, `Distribution.md` for installing and updating)
  - `Requirements/Technical/`: how it's built (`Technical Requirements.md`, `Distribution.md` for packaging, signing and releases)
  - `Technical Decisions.md`: why it's built that way
- `Guides/`: how-tos for users (getting started, writing a routine, adding a plugin)

## Formatting
- Every page starts with `Back to [[Home]].`, plus links to related pages (e.g. `See also: [[Spec/Technical Decisions|Technical Decisions]]`)
- Links are Obsidian wiki links with the vault-relative path: `[[Spec/Technical Decisions|Technical Decisions]]`. Markdown links only from outside the vault (README)
- Requirements are bullets grouped under `##` sections by area of the app, with a **bold lead** for the subject. Tables for lists of fields. Commands, files and keys in backticks
- Decisions are `## Decision: <what was chosen>`, a short paragraph, then `### Rejected` with each alternative as `- **Name**: why not`
- Written as the owner, in the first person ("me", "my routines")

## Rules
- **Requirements state unmoving facts about the app.** Never progress markers: no ✅, "implemented", "done", "not yet", version, release or block tags. The spec describes the app as it is; releases are git tags
- **Requirements say what; decisions say why.** A reason or rejected alternative belongs in Technical Decisions, not inline in requirements
- **Each fact once.** Before adding, find where the subject already lives and extend it; don't add a second description elsewhere
- Keep it concise: no examples or history unless they define the rule
- Docs match the app: when code changes a documented fact, update the doc in the same change
