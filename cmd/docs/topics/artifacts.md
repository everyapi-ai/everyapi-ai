# Publishing HTML reports

`everyapi artifacts` publishes a single self-contained HTML file to a share URL that anyone with the link can open for 30 days. It is the surface behind the completion-report standard that `everyapi use` injects into Claude Code, Codex, OpenCode and Kilo, and it works the same way when you run it by hand.

```
everyapi artifacts share [--json] <file.html>
everyapi artifacts list [--json]
everyapi artifacts update [--json] <url> <file.html>
everyapi artifacts delete [--json] <url>
```

`share` prints the URL. `update` replaces the content behind an existing URL, so a report can be revised without invalidating a link you already handed out. `delete` revokes it immediately. `list` shows what you currently have published, with expiry.

One file, up to 5 MiB, `.html` or `.htm`. There is no second request for assets — whatever the file does not contain, the reader will not see.

## How a report is served

The share URL renders an EveryAPI frame — title, expiry, **Open document**, abuse link — around your document, and your document is served from a **separate origin** inside a sandboxed iframe. That separation is the security model: your report cannot read the page hosting it, cannot reach your session, and cannot navigate the browser away from EveryAPI.

**Open document** in that frame is the report on its own, at its own address, for printing, saving, or reading on a narrow screen. It is the same isolated origin under the same policy — nothing is unlocked by opening it directly.

The same separation is why the viewer is strict about what a report may do.

## What the viewer blocks

The content response carries `default-src 'none'` and a sandbox without same-origin privilege. In practice:

- An external stylesheet or script is blocked. Inline it in a `<style>` or `<script>` tag.
- An external image or webfont is blocked. Use a `data:` URI, or a system font stack.
- `fetch`, `XMLHttpRequest` and `WebSocket` are blocked: `connect-src 'none'`, there is no network.
- `localStorage`, `sessionStorage` and `indexedDB` are unavailable: the frame has an opaque origin.

Inline `<style>` and `<script>` both work, and so do `data:` and `blob:` images, fonts and media. A report can be fully interactive; it just cannot reach outward.

**None of these produce an error you will see.** The publish succeeds, the URL works, and the report is quietly wrong for whoever opens it. `everyapi artifacts share` scans for the two commonest cases and warns on stderr before uploading — it never blocks the publish, and it never touches the `--json` output on stdout.

## Colours

The viewer serves your document on a white canvas and forces `color-scheme: light`, so a report cannot end up as white text on white background because its reader happens to run a dark OS theme.

Do not lean on that. Set `background-color` and `color` explicitly on `html` and `body`. The viewer supplies a readable pairing only as a floor, and any palette you declare yourself — including a deliberately dark one — overrides it untouched.

## Links

Links in a report are rewritten to open in a new tab. Without that, clicking one would navigate the iframe itself: the report would be replaced in place by the destination, with the EveryAPI frame still announcing the report and no way back.

In-page anchors (`href="#section"`) keep working as anchors. Write real links; you do not need to add `target` yourself.

## Verify before you share the URL

Publishing succeeds long before anyone finds out whether the report reads correctly, so look at the result once before passing the link on.

Note what you are looking at. The share URL returns the **frame**, not the report: fetching it and finding your own title proves only that the artifact exists. The document lives at the frame's iframe source, which is what **Open document** points at.

```
url=$(everyapi artifacts share report.html --json | jq -r .url)
curl -s "$url" | grep -o 'iframe .*src="[^"]*"'
```

In a browser, just open `$url` and read it. Check it in a dark colour scheme too if your report styles anything itself.
