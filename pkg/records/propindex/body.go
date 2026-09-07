// Omnipus — ADR-068 D24 / spec FR-130, FR-131: the two body projections that
// back `file.tags`, `file.links` and `file.embeds`.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `file.tags`, `file.links` and `file.embeds` are facts about a note's TEXT, not
// about its declared properties, so nothing in rows.go's schema-driven
// derivation can produce them. They are derived here, in pure Go, from the same
// bytes ExtractTasks reads — and, like every other derivation in this package,
// they are computed BEFORE SQLite sees anything, because a projection SQLite
// performs is a projection SQLite could be asked to compare.
//
// The two child tables they populate are streamed by their OWN statements
// (FR-131). They are never joined to each other or to note_props: three LEFT
// JOINs against one parent is a Cartesian product, and 30 properties x 10 tags x
// 40 links is 12,000 rows for a note that yields 30 today. At B1's 50,000
// candidates that is a hang, not a slowdown, and it is the exact fan-out D16.6
// already fixed once.
//
// MASKING COMES FIRST, and it is the part that is easy to skip. A `#tag` inside
// a fenced code block is a code comment, and a `[[Bed 1]]` inside FRONTMATTER is
// a declared relation property that note_relations already holds. Counting
// either one puts a value in the index that the note does not contain in the
// sense the operator means — and `file.tags` is a filter target, so a phantom
// tag is a record appearing in an answer it does not belong in.
// ---------------------------------------------------------------------------

// TagRow is one row of the note_tags child table — one tag of one note.
//
// Tags are stored FULLY QUALIFIED (`project/omnipus`, not `omnipus`), because
// that is the string the operator wrote and the one FR-134's `file.hasTag(x)`
// translation matches with `{file.tags, =, x}` OR `{file.tags, LIKE, x/%}`. A
// truncated tag would make the hierarchy-aware half of that translation
// meaningless.
//
// The leading `#` is NOT stored. It is syntax marking where a tag starts in
// running text, not part of the tag's name, and a frontmatter `tags: [a]`
// carries no `#` at all — storing it for one source and not the other would
// make the same tag two different values depending on where it was written.
type TagRow struct {
	// Elem is the tag's position in the note's tag set, in the order the tags
	// were found: frontmatter first, then the body in reading order.
	Elem int
	Tag  string
}

// LinkRow is one row of the note_links child table — one wikilink or embed.
//
// DELIBERATE DEVIATION FROM FR-131'S LITERAL COLUMN LIST, stated here rather
// than discovered later. The requirement names four columns — note_id, elem,
// target, embed — and this table carries three more: heading, display and raw,
// exactly as its sibling note_relations does.
//
// The reason is the declared consumer. `records.FileLinkRow` (filemeta_backlinks.go)
// holds Target, Heading, Display, Raw and Embed, and its Wikilink() method
// renders Heading and Display into the link value the comparator and the
// renderer see. Storing only the target would leave those two empty on every
// row, so every `file.links` value would lose the display text the note
// actually wrote — silently, since a link with no display text is a perfectly
// ordinary link and nothing would look wrong.
//
// The four-column list is a MINIMUM that reads as an exhaustive one; the
// consumer's type is the more specific statement of the same requirement.
type LinkRow struct {
	// Elem is the link's position in the note's body, in reading order. Links
	// are NOT de-duplicated: a note that links to the same target twice
	// genuinely contains two links, and collapsing them would make
	// `file.links` disagree with the note.
	Elem    int
	Target  string
	Heading string
	Display string
	Raw     string
	// Embed distinguishes `![[x]]` from `[[x]]`. It is the ONLY difference
	// between `file.links` and `file.embeds`, which is why the two share one
	// table and one stream rather than becoming a second fan-out.
	Embed bool
}

// frontmatterTagKeys are the frontmatter keys that hold tags.
//
// Both spellings, because both are in real vaults: Obsidian's own property UI
// writes `tags`, and `tag` is the older singular form it still reads.
var frontmatterTagKeys = []string{"tags", "tag"}

// inlineTag matches a `#tag` in running text.
//
// The leading group is a real captured character rather than a look-behind
// (RE2 has none), and it is what keeps `https://example.com/x#anchor` and
// `C#` out of the tag set: a `#` preceded by a letter, digit or tag character
// is not the start of a tag.
var inlineTag = regexp.MustCompile(`(^|[^\p{L}\p{N}_/-])#([\p{L}\p{N}_/-]+)`)

// wikilink matches `[[target]]` and `![[target]]` in running text.
var wikilink = regexp.MustCompile(`(!?)\[\[([^\[\]\n]+)\]\]`)

// codeFence matches an opening or closing fenced-code-block line.
var codeFence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// ExtractTags projects a note's frontmatter and body into tag rows.
//
// Frontmatter first, then the body, in reading order. The set is
// DE-DUPLICATED, keeping the first spelling seen: `#project` written four times
// in a note is one tag, and `file.tags` is the note's tag SET — four rows would
// make `file.tags` behave differently for a note that repeats itself, which is
// a difference no operator asked for.
//
// De-duplication is byte-exact, deliberately. `#Project` and `#project` are two
// tags here, and whether they compare equal is R-9's business, in the Go
// comparator, with the fold rules that ruling names — not a decision this
// derivation makes silently by lowercasing on the way in.
func ExtractTags(rec records.Record, src []byte) []TagRow {
	var (
		out  []TagRow
		seen = map[string]struct{}{}
	)
	add := func(tag string) {
		tag = normaliseTag(tag)
		if tag == "" {
			return
		}
		if _, dup := seen[tag]; dup {
			return
		}
		seen[tag] = struct{}{}
		out = append(out, TagRow{Elem: len(out), Tag: tag})
	}

	for _, key := range frontmatterTagKeys {
		n, ok := rec.Frontmatter.Get(key)
		if !ok {
			continue
		}
		switch n.Kind {
		case records.KindScalar:
			// `tags: work, home` is ONE YAML scalar holding two tags. A vault
			// written through Obsidian's own UI produces this form, so reading
			// it as a single tag named "work, home" would lose both.
			for _, part := range strings.FieldsFunc(n.Text, func(r rune) bool {
				return r == ',' || r == ' ' || r == '\t'
			}) {
				add(part)
			}
		case records.KindSequence:
			for _, item := range n.Items {
				if item.Kind == records.KindScalar {
					add(item.Text)
				}
			}
		}
	}

	for _, m := range inlineTag.FindAllStringSubmatch(maskedBody(src), -1) {
		add(m[2])
	}
	return out
}

// normaliseTag strips the syntax a tag may be written with and rejects what is
// not a tag at all.
//
// A tag of digits only is NOT a tag — `#2026` in a note is a heading fragment
// or a number, and Obsidian's own rule is the same. It is rejected here rather
// than stored and filtered later, because a phantom tag in the index is a
// record that appears in an answer it does not belong in.
func normaliseTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	s = strings.Trim(s, "/")
	if s == "" {
		return ""
	}
	hasLetter := false
	for _, r := range s {
		if !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '/' {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return ""
	}
	return s
}

// ExtractLinks projects a note's body into link rows.
//
// The BODY only. A wikilink in frontmatter is a declared relation value and is
// already stored, once, in note_relations (D5) — reading it here as well would
// put the same edge in two tables with two different meanings, and a caller
// assembling both would count it twice.
func ExtractLinks(src []byte) []LinkRow {
	var out []LinkRow
	for _, m := range wikilink.FindAllStringSubmatch(maskedBody(src), -1) {
		link, ok := records.ParseWikilink("[[" + m[2] + "]]")
		if !ok {
			continue
		}
		out = append(out, LinkRow{
			Elem:    len(out),
			Target:  link.Target,
			Heading: link.Heading,
			Display: link.Display,
			// The RAW form carries the embed marker, so a report quoting the
			// link shows what the note wrote rather than a reconstruction that
			// turns every embed into a plain link.
			Raw:   m[0],
			Embed: m[1] == "!",
		})
	}
	return out
}

// maskedBody blanks out everything in a note that is not running text: its
// frontmatter block, its fenced code blocks, and its inline code spans.
//
// Blanking rather than deleting keeps every remaining byte at its original
// offset and every line at its original number, so nothing downstream has to
// reason about two coordinate systems.
func maskedBody(src []byte) string {
	s := strings.TrimPrefix(string(src), "\uFEFF")
	lines := strings.Split(s, "\n")

	// The frontmatter block, by the SAME rules records.ParseFrontmatter uses:
	// the opening `---` must be the very first line, and the block ends at the
	// next `---` or `...`. A file that is nothing but an unterminated block is
	// all frontmatter, which is the normal shape of a metadata-only note.
	if len(lines) > 1 && isFrontmatterFence(lines[0]) {
		lines[0] = ""
		for i := 1; i < len(lines); i++ {
			end := isFrontmatterFence(lines[i]) || isFrontmatterDocEnd(lines[i])
			lines[i] = ""
			if end {
				break
			}
		}
	}

	// Fenced code blocks. The closing fence must be at least as long as the
	// opening one and of the same character, which is what lets a ```` ``` ````
	// example live inside a ```` ```` ```` block.
	var fence string
	for i, line := range lines {
		m := codeFence.FindStringSubmatch(strings.TrimRight(line, " \t\r"))
		switch {
		case fence == "" && m != nil:
			fence = m[1]
			lines[i] = ""
		case fence != "":
			if m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) {
				fence = ""
			}
			lines[i] = ""
		}
	}

	return maskInlineCode(strings.Join(lines, "\n"))
}

func isFrontmatterFence(line string) bool {
	return strings.TrimRight(line, " \t\r") == "---"
}

func isFrontmatterDocEnd(line string) bool {
	return strings.TrimRight(line, " \t\r") == "..."
}

// maskInlineCode blanks the contents of every inline code span, along with its
// delimiters, leaving newlines in place so line numbers survive.
//
// A backtick run opens a span that is closed by a run of the SAME length, which
// is CommonMark's rule and the reason “a `b` c“ is one span rather than two.
// An unclosed run is not a span at all and is left alone.
func maskInlineCode(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		open := i
		for i < len(b) && b[i] == '`' {
			i++
		}
		runLen := i - open
		closeAt := -1
		for j := i; j < len(b); {
			if b[j] != '`' {
				j++
				continue
			}
			start := j
			for j < len(b) && b[j] == '`' {
				j++
			}
			if j-start == runLen {
				closeAt = j
				break
			}
		}
		if closeAt < 0 {
			continue
		}
		for k := open; k < closeAt; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
		i = closeAt
	}
	return string(b)
}
