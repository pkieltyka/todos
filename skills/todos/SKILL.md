---
name: todos
description: Inspect todos version and help; the full agent workflow is being prepared.
---

# todos (foundation placeholder)

Read commands and the interactive TUI are available; issue changes are still under construction.
This placeholder is embedded in the binary and will be replaced by the full
agent workflow in the release-readiness phase.

Available commands:

```sh
todos --help
todos help version
todos version --json
todos skill
```

Bare `todos` opens the interactive TUI. The four commands above do not
contact GitHub, inspect a repository, read credentials, or change any files.
