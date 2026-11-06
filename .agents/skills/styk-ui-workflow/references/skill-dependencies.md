# Design skill dependencies

Read this reference for plugin discovery failures or team setup, rather than on
every UI edit. The project skill is checked into `.agents/skills`; upstream
plugins remain developer installations. Do not commit plugin caches, personal
configuration, credentials or machine-specific paths.

## Authoritative packages

| Plugin | Source | Skills used here |
| --- | --- | --- |
| `frontend-design@claude-plugins-official` | [Anthropic official marketplace](https://github.com/anthropics/claude-plugins-official/tree/main/plugins/frontend-design) | `frontend-design` |
| `design@knowledge-work-plugins` | [Anthropic knowledge-work marketplace](https://github.com/anthropics/knowledge-work-plugins/tree/main/design) | `design-system`, `design-critique`, `accessibility-review`, `ux-copy`, `user-research`, `research-synthesis`, `design-handoff` |

Prefer the official plugin over an unrelated local skill with the same name.
Discover the installed version and actual skill names through the host's catalog;
do not hard-code cache versions or assume that slash-command aliases in an
upstream README work in every agent host. Read the actual skill files.

## Setup when requested

Use the host's supported plugin manager. In Codex installations with plugin CLI
support, inspect existing marketplaces before adding a missing one:

```sh
codex plugin marketplace list
# Add only the marketplaces that are missing:
codex plugin marketplace add anthropics/claude-plugins-official
codex plugin marketplace add anthropics/knowledge-work-plugins
codex plugin add frontend-design@claude-plugins-official --json
codex plugin add design@knowledge-work-plugins --json
```

Verify both plugins are installed and enabled and that all eight skills are
discoverable. Start a new session if the host still shows an older catalog.
Use the installed supported release for ordinary work. Upgrades belong to an
explicit setup/update task; do not refresh marketplaces on every UI edit.
For another host, use its documented installation flow rather than copying
Codex-specific commands or cache directories.

## Missing dependency or tool

1. Check the available skill catalog and plugin installation metadata. Read the
   installed skill by the path returned there if the host does not expose an
   invocation shortcut. Do not search arbitrary personal files for instructions.
2. If the skill is absent and source retrieval is available, read its official
   `skills/<skill-name>/SKILL.md` under the source directory linked above. Read
   referenced resources only when needed. Disclose this as an upstream-source
   fallback, rather than claiming the plugin is installed or invoked.
3. If the official source is also inaccessible, name the missing skill and follow
   the project workflow for useful work that remains possible. Report the
   incomplete skill coverage. Do not silently substitute another plugin or
   install/upgrade tools outside the authorized task.

Figma, analytics and research connectors are optional. Screenshots, the local
app and supplied evidence can support the workflow without them. If a check
requires an unavailable browser or assistive technology, state the unverified
behavior. Never fabricate screenshots, research, scan results or user feedback.

## Host and review references

- [Codex skill discovery and invocation](https://learn.chatgpt.com/docs/build-skills)
- [Codex project instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
- [W3C accessibility evaluation guidance](https://www.w3.org/WAI/test-evaluate/)

The `AGENTS.md` requirement and the skill guide agent behavior. They are not a
runtime policy engine and cannot prove that an agent followed every step. Review
the implementation and its evidence; do not treat a list of skill names as a
quality gate.
