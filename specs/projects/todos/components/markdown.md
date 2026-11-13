---
status: complete
---

# Component: markdown (`internal/markdown`)

Sanitises untrusted text from GitHub and renders Markdown for a terminal.
Used by `tui` (bodies, comments, previews, every short field) and by `cli`
(`todos view` and any text output that prints GitHub text). Owner:
@frontend-dev.

`architecture.md` wins where they differ. References: (5.2) = functional
spec, (UI 1) = ui_design.md, (A1.5) = architecture section 1, principle 5.
Facts marked *(read)* were checked in the source of the pinned versions,
without running them. **UNVERIFIED** marks what I could not check.

---

## 1. Role and rules

- **The only way GitHub text reaches a terminal** (A1.5). Every title, label
  name, login, branch name, project name, body and comment goes through
  `SanitizeLine` or `Render` first, in both faces.
- **No imports of ours** (A2). The API takes and returns strings, so `tui`
  and `cli` use it without `markdown` knowing `domain`.
- **Fail closed.** If a rendered result contains anything other than colour
  and hyperlink sequences, it is thrown away and plain sanitised text is shown
  instead (5).
- **Safe for concurrent use.** `tui` renders from many Cmds at once.

---

## 2. API

```go
package markdown

// SanitizeLine makes a one-line string from GitHub safe and flat:
// titles, label names, logins, branch names, project names, error messages from GitHub.
func SanitizeLine(s string) string

// SanitizeBody makes Markdown source from GitHub safe to parse.
// Render calls it; callers need it only to print a raw body (cli, piped).
func SanitizeBody(s string) string

type Style int
const (
    Dark  Style = iota // glamour DarkStyleConfig
    Light              // glamour LightStyleConfig
    Plain              // glamour NoTTYStyleConfig: no colour, ASCII decorations
)

type Options struct {
    BaseURL   string // "https://github.com/OWNER/REPO", for relative links
    CacheSize int    // entries; 0 → 128
}

type Key struct {
    Hash  uint64 // FNV-64a of the raw source
    Width int
    Style Style
}

type Renderer struct { /* unexported: LRU, glamour instances, mutexes */ }

func NewRenderer(o Options) *Renderer

// Key is cheap and never blocks.
func (r *Renderer) Key(src string, width int, s Style) Key

// Cached is a non-blocking lookup, safe to call from Bubble Tea's Update.
func (r *Renderer) Cached(k Key) (string, bool)

// Render sanitises src, rewrites <img> tags, renders with glamour at exactly
// width cells, checks the output, caches it and returns it. It blocks.
// The tui calls it inside a tea.Cmd; the cli calls it directly.
func (r *Renderer) Render(src string, width int, s Style) (string, error)
```

- `Render` takes **raw** source, so no caller can forget to sanitise.
- The cache key hashes the raw source. Sanitising is deterministic, so equal
  raw input always gives equal output.
- An `error` from `Render` is returned only for a glamour failure. The
  plain-text fallback of section 5 is not an error: it returns the fallback
  text and `nil`.

---

## 3. `SanitizeBody`: rules, in order

| # | Rule | Why |
|---|---|---|
| 1 | Invalid UTF-8 → `strings.ToValidUTF8(s, "�")` | every later step works on runes |
| 2 | `\r\n` → `\n`, then a lone `\r` → `\n` | a bare CR moves the terminal cursor |
| 3 | Remove every well-formed escape sequence (CSI, OSC, DCS, APC, PM, SOS, two-byte ESC) with `ansi.Strip` (x/ansi v0.11.9, *read*) | colours, cursor moves, OSC 52 clipboard writes, OSC 8 links |
| 4 | Delete every remaining rune in U+0000–U+0008, U+000B–U+001F, U+007F–U+009F. Keep `\t` and `\n` | a lone ESC, BEL, and C1 controls as runes (U+009B is a one-character CSI) |
| 5 | Replace `&#[0-9]+;` and `&#[xX][0-9a-fA-F]+;` with `�` when the value is in a rule 4 range or above U+10FFFF | see below |
| 6 | Delete bidi controls U+202A–U+202E and U+2066–U+2069 | disguised text ("Trojan Source"); not a terminal control, but misleading |

**Rule 5 is necessary.** Rules 1–4 clean the *source*, but rendering decodes
entities afterwards:

- goldmark decodes numeric character references in Markdown text into the
  rune itself. `util.ToValidRune` rejects only 0 and invalid runes, so
  `&#27;` becomes a real ESC *(read, goldmark `util/util.go`)*.
- glamour passes raw HTML through bluemonday and then
  `html.UnescapeString` (`ansi/context.go`, `SanitizeHTML`, *read*), which
  decodes entities again.

Without rule 5, a body containing `&#27;[2J` would clear the user's screen.
Named references cannot produce control characters, so only numeric ones are
handled.

`SanitizeLine(s)`: `SanitizeBody(s)`, then `\n` and `\t` → space, then runs
of whitespace collapse to one space, then trim. Entities are not decoded for
lines, so rule 5's output (`�`) is shown as is.

---

## 4. Raw HTML and images (5.2)

**HTML is handled by glamour, not by us.** In the pinned glamour, every
`HTMLBlock` and `RawHTML` node goes through `bluemonday.StrictPolicy()`
(`ansi/elements.go`, `ansi/context.go`, *read*; the same in v2.0.1). That
policy removes every tag and every HTML comment and keeps the text between
tags. This is exactly (5.2):

- HTML comments (issue templates) are dropped.
- `<details><summary>Logs</summary>…</details>` shows `Logs…`.
- Other tags are removed, and their text is kept.

**`<img>` tags need a pre-pass**, because bluemonday removes them entirely,
while (5.2) requires that "images appear as their link, whether written as
Markdown or as an HTML `<img>` tag". This pre-pass is part of v1.

### 4.1 The `<img>` pre-pass

```go
// rewriteImages replaces every <img> tag inside raw HTML with visible text.
// It runs on sanitised source, before glamour.
func rewriteImages(src string) string
```

1. Parse `src` with goldmark, configured exactly as glamour configures it:
   `extension.GFM`, `extension.DefinitionList`, `parser.WithAutoHeadingID()`
   (glamour `glamour.go`, `NewTermRenderer`, *read*). Using the same parser
   means "inside HTML" is decided exactly as glamour will decide it. Code
   blocks and code spans are never `HTMLBlock` or `RawHTML` nodes, so code is
   never touched.
2. Walk the AST and collect the byte segments of every `ast.HTMLBlock` (its
   `Lines()`) and `ast.RawHTML` (its `Segments`).
3. Inside each segment, tokenise with `golang.org/x/net/html` (already a
   dependency through bluemonday). For each `img` start or self-closing tag,
   read `src` and `alt`.
4. Replace the tag's bytes with the text `Image: ALT → SRC`, where:
   - `ALT` and `SRC` are `SanitizeLine`d. The tokenizer decodes entities, so
     this repeats rule 5 for attribute values.
   - `ALT` defaults to `image` when empty or absent. GitHub's pasted
     screenshots use `alt="image"`.
   - `<`, `>` and `&` in the inserted text are written as `&lt;`, `&gt;` and
     `&amp;`. bluemonday keeps them as text, and glamour's
     `html.UnescapeString` turns them back into literal characters.
   - An `img` without `src` is replaced with nothing.
5. Apply the replacements from the end of the source to the start, so
   earlier offsets stay valid.

The text form matches how glamour shows a Markdown image (`ImageText`
format `Image: {{.text}} →` followed by the URL, `ansi/image.go`, *read*). The
two kinds of image look alike.

---

## 5. The post-render guard

```go
// guard reports whether out contains only SGR sequences, OSC 8 hyperlinks,
// printable text, "\n" and spaces.
func guard(out string) bool
```

It walks `out` with x/ansi's parser (`ansi.NewParser`, *read*). It accepts:

- CSI sequences whose final byte is `m` (SGR)
- OSC sequences with command `8` (hyperlinks)
- `\n`
- printable runes

Anything else (another CSI, any other OSC, DCS, a C0 or C1 control, a lone
ESC) makes `guard` return false.

When `guard` fails, `Render` returns `ansi.Wordwrap(SanitizeBody(src), width,
"")` with no styling, and caches that. That case means the sanitiser has a
hole. `FuzzRender` exists to find such holes (8).

---

## 6. glamour configuration

```go
func newGlamour(width int, s Style, baseURL string) (*glamour.TermRenderer, error) {
    var cfg ansi.StyleConfig
    switch s {
    case Dark:  cfg = styles.DarkStyleConfig
    case Light: cfg = styles.LightStyleConfig
    case Plain: cfg = styles.NoTTYStyleConfig
    }
    zero := uint(0)
    cfg.Document.Margin = &zero          // output width == wrap width; callers place the text
    return glamour.NewTermRenderer(
        glamour.WithStyles(cfg),
        glamour.WithWordWrap(width),
        glamour.WithEmoji(),             // GitHub :shortcodes:
        glamour.WithBaseURL(baseURL),    // relative links → the repository
        glamour.WithInlineTableLinks(true),
    )
}
```

- Version: `charm.land/glamour/v2 v2.0.2-0.20261015065741-010543cac1a0`, the
  one go-gh v2.17.0 requires (A10). It builds with lipgloss v2.1.0
  (verified).
- `WithStyles`, `WithWordWrap`, `WithEmoji`, `WithBaseURL`,
  `WithInlineTableLinks`, `DarkStyleConfig`, `LightStyleConfig` and
  `NoTTYStyleConfig` (an alias of `ASCIIStyleConfig`) all exist in that
  version *(read)*. `StyleBlock.Margin` is `*uint` (`ansi/style.go`, *read*).
  There is no `WithAutoStyle` in v2. The caller chooses the style.
- **Hyperlinks** keep the default `HyperlinkModeAuto`: link text, then the URL
  with an OSC 8 link on it. Terminals without OSC 8 show the URL, so this
  degrades safely.
- **Code blocks** are highlighted by chroma through glamour, for the dark and
  light styles. `Plain` has no colour.
- glamour pads every line to the wrap width with styled spaces (seen in a
  test render). That is why `width` must equal the space the caller draws
  into, and why a resize means a new render.
- **Thread safety of `TermRenderer` is UNVERIFIED.** So each instance is used
  under its own mutex (7).

---

## 7. Cache and concurrency

```go
type Renderer struct {
    opts  Options
    mu    sync.Mutex
    lru   *lru                    // Key → string, CacheSize entries
    tr    map[trKey]*lockedTR     // one glamour instance per (width, style); at most 4, LRU-evicted
}
type trKey    struct{ width int; style Style }
type lockedTR struct{ mu sync.Mutex; r *glamour.TermRenderer }
```

The `Render(src, width, s)` pipeline:

1. `k := Key(src, width, s)`. On a cache hit, return it.
2. `clean := rewriteImages(SanitizeBody(src))`.
3. Take the instance for `(width, s)`, creating it if needed, then lock it and
   call `r.Render(clean)`.
4. If `guard(out)` fails, `out` becomes the plain fallback (5).
5. Trim trailing blank lines. Store `out` under `k` and return it.

- `r.mu` guards only the maps, never a glamour call, so renders of different
  widths or styles run in parallel.
- Two concurrent renders of the same key may both run. The second store wins
  and is identical. There is no single-flight; this is the boring choice
  (A1.7).
- At 128 entries and about 50 KB average (an estimate), the cache stays
  within a few MB.

The TUI keeps bodies and comments under separate keys, so a new comment
renders only itself. The CLI renders once per command and needs no cache, but
uses the same `Renderer`.

---

## 8. Use by the CLI (`todos view`)

UI 8.1: Markdown is rendered when writing to a terminal and printed raw when
piped.

| Output | Call |
|---|---|
| Terminal | `Render(body, termWidth-2, style)`. `style` comes from `TODOS_THEME` if set, else `lipgloss.HasDarkBackground(os.Stdin, os.Stdout)` (lipgloss v2.1.0, *read*). With `NO_COLOR`, `Plain` |
| Piped / redirected | `SanitizeBody(body)` printed as is. Raw Markdown, still free of control characters |
| `--json` | No call. JSON escapes control characters itself, and agents get GitHub's exact text |

Titles and other short fields in CLI text output go through `SanitizeLine`.

---

## 9. Test plan

All tests are table tests or fuzz tests, with no network and no terminal.

**sanitiser**
- `TestSanitizeBody_table`. One row each:
  - CSI colour, cursor move, `ESC[2J`
  - OSC 52 clipboard write, OSC 8 link, DCS, APC
  - lone ESC, BEL, C1 U+009B as a rune, DEL
  - `&#27;`, `&#x1b;`, `&#X1B;`, `&#155;`, `&#x9B;`, `&#0;`, `&#1114112;`
  - `&#10;` and `&#9;` kept, `&amp;` kept
  - bidi controls
  - CRLF and lone CR
  - invalid UTF-8
  - tabs and newlines kept, emoji and CJK kept
- `TestSanitizeLine_flattensAndTrims`
- `TestSanitizeBody_idempotent`

**images**
- `TestRewriteImages_pastedScreenshotBlock` (`<img width="512" alt="image" src="https://github.com/user-attachments/…">`)
- `TestRewriteImages_inlineRawHTML`
- `TestRewriteImages_untouchedInFencedCode`
- `TestRewriteImages_untouchedInCodeSpan`
- `TestRewriteImages_untouchedInIndentedCode`
- `TestRewriteImages_altDefaultsToImage`
- `TestRewriteImages_noSrcRemoved`
- `TestRewriteImages_attributeEntitiesSanitised` (`alt="&#27;[31m"`)
- `TestRewriteImages_escapesAngleBrackets`

**rendering**
- `TestRender_htmlCommentDropped`
- `TestRender_detailsKeepsText`
- `TestRender_markdownImageShowsLink`
- `TestRender_htmlImageShowsLink`
- `TestRender_widthEqualsWrap` (no line wider than `width`, at 40, 80, 100)
- `TestRender_plainHasNoSGR`
- `TestRender_guardFallbackIsPlainWrapped` (inject a renderer output containing `ESC[2J`)
- `TestRender_codeBlockHighlightedDarkAndLight`
- `TestRender_relativeLinkUsesBaseURL`
- `TestRender_emojiShortcode`
- `FuzzRender_onlySGRandOSC8`: every input, at width 60 and every style, passes `guard` *without* the fallback
- `FuzzSanitizeBody_noControls`: the output has no rune in the rule 4 ranges and no ESC

**cache and concurrency**
- `TestRenderer_cacheHitByKey`
- `TestRenderer_styleSwitchMisses`
- `TestRenderer_widthSwitchMisses`
- `TestRenderer_lruEvicts`
- `TestRenderer_concurrentRendersRace` (`go test -race`, 32 goroutines, mixed widths and styles)

---

## 10. Dependencies

These rows of A10, unchanged:

- `charm.land/glamour/v2` v2.0.2-0.20261015065741-010543cac1a0
- `github.com/charmbracelet/x/ansi` v0.11.9

Used indirectly through glamour, at the versions glamour requires, with no
pins of our own: `github.com/yuin/goldmark` (the `<img>` pre-pass parses with
it) and `golang.org/x/net/html` (the attribute tokenizer). `lipgloss` is used
only by `cli` for `HasDarkBackground`. `markdown` itself does not import it.
