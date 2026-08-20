# CLI translation files

This directory holds the CLI's translation tables — one `.toml` file per language. They're embedded into the binary at build time via `//go:embed locales/*.toml` and looked up by dotted key (`i18n.T("token.no_tokens")`).

If you want to **improve an existing translation** or **add a new language**, this is the single place to edit. No Go code changes needed in either case.

## Languages currently shipped

| Code | Language | File |
|---|---|---|
| `en` | English (source of truth) | [`en.toml`](en.toml) |
| `zh` | 简体中文 | [`zh.toml`](zh.toml) |
| `zh-TW` | 繁體中文 | [`zh-TW.toml`](zh-TW.toml) |
| `ja` | 日本語 | [`ja.toml`](ja.toml) |
| `ko` | 한국어 | [`ko.toml`](ko.toml) |
| `es` | Español | [`es.toml`](es.toml) |
| `de` | Deutsch | [`de.toml`](de.toml) |
| `fr` | Français | [`fr.toml`](fr.toml) |

The supported set is discovered at startup by scanning this directory for `*.toml` — the list above is documentation, not a registry, so a new file ships as soon as it is embedded.

All non-`en` files were machine-translated in a first pass. **Native speaker refinement is the most useful PR you can send.**

## Improving an existing translation

1. Find the key whose translation reads wrong. The CLI's verbatim strings live under `[section] key = "..."` lines; help blocks live under `[section] usage = '''...'''` triple-quoted multi-line blocks.
2. Edit the value on the right side of `=` (NEVER the key on the left, NEVER the section header).
3. Send a PR. We don't gate refinements on Go tests passing — typo and wording improvements are always welcome.

That's it. The change ships with the next CLI release; users get it via `brew upgrade everyapi` / `everyapi version update` / `go install`.

## Adding a new language

Adding `ja` / `ko` etc. earlier took **zero Go code** beyond two cosmetic lines (a `Lang*` const, a `normalizeLang()` prefix case for system locale auto-detection). The architecture wants more locales.

1. Copy `en.toml` to `<lang>.toml`, where `<lang>` is the ISO-639-1 code (e.g. `pt.toml` for Portuguese, `it.toml` for Italian, `ru.toml` for Russian). Use a region suffix only when the script genuinely differs, the way `zh-TW.toml` does.
2. Translate every value. **Leave keys + section headers alone.**
3. (Optional, for nicer auto-detection): add a `Lang<X>` const + a `case strings.HasPrefix(lang, "<x>"):` to `normalizeLang()` in `clients/cli/internal/i18n/i18n.go`. Without this step the new locale still works via `everyapi settings set language <x>` and `EVERYAPI_LANG=<x>`, but `LANG=<x>_XX.UTF-8` won't auto-detect.
4. (Recommended): also add a corresponding `<lang>.yaml` to `backend/internal/i18n/locales/` so backend API errors render in the same language (otherwise CLI strings translate but API error bodies fall back to English).

Then send a PR. The loader auto-discovers any `.toml` here at the next build (`//go:embed locales/*.toml`).

## Format rules (keep these intact, regardless of language)

- **Preserve every `%`-format verb** verbatim: `%s`, `%d`, `%q`, `%v`, `%.2f`, `%[1]s`, etc. Same count, same types as in `en.toml`. If your target language reorders the substituted args, use indexed verbs like `"%[2]s 中包含 %[1]d"` instead of dropping/swapping positional `%`-verbs.
- **Don't translate**:
  - Command names + flag names (`everyapi token list`, `--name`, `-y`).
  - Product / tool names (`EveryAPI`, `Homebrew`, `Claude Code`, `Codex`, `Gemini`, `Stripe`, `Docker`, `Ollama`).
  - File-extension / format names (`.json`, `.toml`, `.yaml`).
  - URLs and shell paths.
  - Technical jargon with no widely-accepted local equivalent (`URL`, `JSON`, `OAuth`, `TLS`, `MCP`, `JWT`, `API key`).
- **Section headers in USAGE blocks** (`USAGE`, `SUBCOMMANDS`, `FLAGS`, `EXAMPLES`, `NOTES`, `KEYS`) translate to whatever's standard in your language's CLI help convention.
- **Multi-line strings** use TOML's triple-quoted literal form (`'''…'''`, not `"""…"""`). The single-quoted form doesn't interpret backslash escapes — handy for help text with embedded backticks. The leading newline immediately after the opening `'''` is trimmed by the parser, so write the block flush-left.

## Verifying your changes locally

From `clients/cli/`:

```bash
go vet ./internal/i18n/...
go build ./...
go test -race -timeout 1m ./internal/i18n/...

# render the launcher in your locale:
go run . settings set language <lang>
go run . help
```

Or without setting:

```bash
EVERYAPI_LANG=<lang> go run . help
```

If you see the raw key (e.g. literally `token.usage`) where translated text should be, the loader couldn't find that key. Most common cause: a typo in the key name or wrong section.

## File format quick reference

```toml
# A section
[token]
no_tokens = "No tokens yet. Use 'everyapi token create --name <n>'."
count = "%d token(s):"            # %d is required

# Nested sub-section becomes the dotted key token.label.name
[token.label]
name = "name:"
key  = "key:"

# Multi-line USAGE block; loader strips the leading newline.
usage = '''
everyapi token — manage relay API tokens

USAGE
  everyapi token <subcommand> [flags]
…
'''
```

## Key naming convention

`<package_name>.<short_descriptor>` — e.g. `token.no_tokens`, `seller.channel_updated`, `doctor.section.account`.

For cross-cutting strings shared across packages: `common.<x>` (e.g. `common.canceled`), `auth.<x>` (login / session prompts).

## Questions

Open an issue on https://github.com/everyapi-ai/everyapi or ping the maintainers. Translation PRs don't need a deep code review — wording quality + format-verb integrity are what we check.
