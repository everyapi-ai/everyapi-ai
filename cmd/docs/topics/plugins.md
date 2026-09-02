# Plugins and marketplaces

`everyapi plugins` is the local plugin lifecycle used by EveryAPI desktop clients. It delegates installation to the user's Codex CLI, then returns a stable EveryAPI JSON contract so callers do not need to spawn Codex or parse `.codex-plugin/plugin.json` themselves.

```
everyapi plugins catalog [--json]
everyapi plugins list [--available] [--json]
everyapi plugins install <plugin@marketplace> [--json]
everyapi plugins remove <plugin@marketplace> [--json]
everyapi plugins marketplace list [--json]
everyapi plugins marketplace add <source> [options] [--json]
everyapi plugins marketplace update [name] [--json]
everyapi plugins marketplace remove <name> [--json]
```

`catalog --json` returns installed and available plugins together with configured marketplaces. Each plugin includes presentation metadata, component counts and capability flags derived from its manifest. Manifest paths are canonicalized and kept inside the plugin root before files are inspected.

`list` shows installed plugins by default. Add `--available` to include entries that can be installed from configured marketplaces.

Marketplace `add` accepts one optional `--ref REF` and up to 32 repeated `--sparse PATH` options.

Install, remove, marketplace add, update and remove are state-changing operations. The CLI validates every plugin selector, marketplace name, Git ref and sparse path before invoking Codex. Install and remove additionally reconcile the current catalog first, so stale or forged UI state cannot select an unavailable plugin. With `--json`, a successful mutation returns the freshly loaded catalog.

Set `EVERYAPI_CODEX_CLI_PATH` to an absolute Codex executable when the desired binary is not on `PATH`.
