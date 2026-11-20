# Templates

Routine files tracked here so anyone can install them as is or adapt them, project-specific ones included. The app ships none yet: this folder is where they go. With none, `procauto templates list` is empty and setup's templates step has nothing to offer.

Templates are grouped by tags, as folders, general to specific:

```
templates/
  standup.prompt           a template for anyone
  personal/
    digest.prompt
  projects/
    acme/                  for members of the acme project only
      standup.prompt
```

A template is a routine file (see the routines folder conventions: `<name>.prompt`, `_<name>.prompt` partials) with two optional extras:
- `automator.parameters`: values asked for at install, stored as per-routine settings (`routines.<name>.params.<param>`), used in the prompt as `{{param}}`
- `automator.platforms`: the OSes it runs on (e.g. `[macos]`); others don't offer it

A template's project is its folder under `projects/` (e.g. `acme`): setup asks which projects you work on, then offers those projects' templates and the ones with no project. `procauto templates list` shows them; `procauto templates install <tags>/<name>` copies one into the routines folder (never over an existing routine). The tags only organise templates; routines on the device stay flat.

Every template here must load cleanly, include `{{> memories}}`, and render with the standard inputs: `go test ./internal/routines` checks them all.
