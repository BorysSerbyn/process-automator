Back to [[Home]]. See also [[Spec/Requirements/Functional/Functional Requirements|Functional Requirements]], [[Guides/Using it from Claude|Using it from Claude]].

What LLM clients can do through the app, and how the agent behaves inside a routine.

## Making routines with Claude
- **Claude as a frontend**: I can create and manage routines by talking to Claude (Claude Code, Claude Desktop or any MCP client): I describe what I want, and Claude writes the routine, picks its servers and tools, sets its schedule and parameters, installs templates, adds plugins, and dry-runs the routine to show me what it would do
- **Claude learns how from the app**: the MCP server tells Claude to read the guide before writing a routine or adding a server, and that what it creates starts paused and untrusted
- **Claude sees what's possible**: the plugins and their tools (and which only read), the templates, my routines and their settings, recent runs and their outcomes, and why something is red
- **Parity with the CLI**: anything the CLI does, Claude can do through the MCP server, except what stays with me (below)
- **I stay in control**:
  - tools that change something ask me before each call
  - a routine Claude creates or changes starts paused and untrusted; only I trust it
  - Claude can list, run, stop and pause programs, but never add or trust one
  - Claude can list, show, fire (dry runs too), pause and stop hooks and watchers, but never add, change or trust one
  - Claude can see the permissions things need and have the OS ask me for one; only I grant it
  - secrets and sign-ins happen in my browser, never through the model
  - uninstalling the app and running the scheduler stay in my terminal
  - every change Claude makes through the server is logged

## The agent inside a routine
- **Only allowed tools**: the agent can call only the tools its routine's file allows, from the servers it lists
- **Unattended**: no one answers questions during a run; the prompt says everything
- **Judgement over rules**: output quality is left to the model's own judgement (avoiding duplicates, respecting what I did by hand, handling weekends and time off); prompts don't spell out quality rules the model already follows
- **No code-side validation and no human review step**: recurring mistakes are corrected in the routine file or with memories, not by bloating prompts pre-emptively
- **Lean prompts**: the model sees only the routine's prompt, its inputs and its allowed tools; token usage is recorded per run so bloat is visible
- **Reporting its outcome**: the agent ends each run by saying whether it did its job, found nothing to do, or failed, and why

## The agent inside a hook
- **Event data is outside data**: what a watcher printed reaches an LLM hook's prompt marked as untrusted, in a block that says it's data, never instructions

## Memories
- **I can add memories**: short, lasting rules the agent follows in future runs (e.g. "Don't count cancelled meetings")
- **Global or per routine**: a memory applies to every routine or to one; an LLM hook has its own too (`memory --routine <hook>`), a program has none
- **User-driven only**: the agent never creates, edits or removes memories, and the model backend's own memory features are off; Claude can add them over MCP only with my approval
- **Kept small**: each memories file has a cap; past it, I'm asked to compact them first. Compacting merges duplicates and drops outdated ones, after showing me the result
- **Facts that must be applied exactly** (e.g. which project maps to which code) belong in the routine's prompt or parameters, not in memories

## Guardrails
- **Trust**: a routine runs for real only while what it can reach is what I trusted; changing that takes the trust away until I review it again
- **Trusting shows what it can do first**: its servers, its write tools and its read tools, so trust is given knowingly
- **Dry runs**: a routine can be test-run to preview what it would do, with every write intercepted; dry runs need no trust
