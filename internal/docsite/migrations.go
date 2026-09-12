// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/ensera-ai/taisce/internal/migrate"
)

// The catalogue is read from the scripts the binary embeds, through the same loader the migrator
// uses, rather than from the files on disk: what a reader sees is what a deployment applies.

type migrationSet struct {
	key, label, dir, applies string
	scripts                  []migrate.Script
}

func renderMigrations(opts Options) (rendered, error) {
	control, err := migrate.LoadControl()
	if err != nil {
		return rendered{}, fmt.Errorf("docsite: %w", err)
	}
	memory, err := migrate.Load()
	if err != nil {
		return rendered{}, fmt.Errorf("docsite: %w", err)
	}
	sets := []migrationSet{
		{"control", "Control namespace", "internal/migrate/control",
			"once per instance, into the fixed `control` namespace that holds the credential registry", control},
		{"memory", "Memory namespace", "internal/migrate/sql",
			"once per instance, into the memory namespace that holds every project's data", memory},
	}
	var out rendered
	var nav, index strings.Builder
	nav.WriteString("- [Migrations](reference/migrations/overview.md)\n")
	index.WriteString("# Migration catalogue\n\n" +
		"Every migration the binary carries, in the order it applies them, with the argument each one's " +
		"header makes and the SQL it runs. Two sequences are independent: one for the control namespace " +
		"and one for the memory namespace. Each migration and its version marker commit in one " +
		"transaction, so a failure leaves the last fully applied version.\n\n" +
		"A migration's name is a sentence saying what becomes true once it has run. Its header is the " +
		"argument for it, written when it was written. Where a later migration or decision changed the " +
		"reasoning, the later one is what holds; the narrative pages under PostgreSQL describe the " +
		"current state.\n")
	for _, set := range sets {
		fmt.Fprintf(&index, "\n## %s\n\nApplied %s. Source: [`%s`](%s).\n\n| Version | Migration | What it says |\n|---|---|---|\n",
			set.label, set.applies, set.dir, sourceURL(opts, "tree", set.dir))
		fmt.Fprintf(&nav, "  - [%s](reference/migrations/%s/overview.md)\n", set.label, set.key)
		var sub strings.Builder
		fmt.Fprintf(&sub, "# %s migrations\n\nApplied %s.\n\n| Version | Migration |\n|---|---|\n", set.label, set.applies)
		for _, s := range set.scripts {
			stage := fmt.Sprintf("reference/migrations/%s/%04d.md", set.key, s.Version)
			title := sentence(s.Name)
			header := commentHeader(s.SQL, "--")
			fmt.Fprintf(&index, "| %04d | [%s](%s) | %s |\n", s.Version, title, relative("reference/migrations/overview.md", stage),
				cell(firstSentence(plain(header))))
			fmt.Fprintf(&sub, "| %04d | [%s](%s) |\n", s.Version, title, relative("reference/migrations/"+set.key+"/overview.md", stage))
			fmt.Fprintf(&nav, "    - [%04d %s](%s)\n", s.Version, title, stage)
			out.pages = append(out.pages, page{Stage: stage, Body: migrationBody(set, s, title, header, opts)})
			out.count++
		}
		out.pages = append(out.pages, page{Stage: "reference/migrations/" + set.key + "/overview.md", Body: sub.String()})
	}
	out.pages = append(out.pages, page{Stage: "reference/migrations/overview.md", Body: index.String()})
	out.nav = nav.String()
	return out, nil
}

func migrationBody(set migrationSet, s migrate.Script, title, header string, opts Options) string {
	file := fmt.Sprintf("%s/%04d_%s.sql", set.dir, s.Version, s.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "# %04d · %s\n\n%s · version %d · [source](%s)\n\n", s.Version, title, set.label, s.Version, sourceURL(opts, "blob", file))
	if header != "" {
		b.WriteString(header + "\n")
	}
	if list := statements(s.SQL); len(list) > 0 {
		b.WriteString("## What it runs\n\n")
		for _, st := range list {
			fmt.Fprintf(&b, "- `%s`\n", st)
		}
		b.WriteString("\n")
	}
	body := strings.TrimRight(s.SQL, "\n")
	fmt.Fprintf(&b, "## SQL\n\n`{schema}` is replaced with the validated namespace name when the migration is applied.\n\n"+
		"<details><summary>%d lines</summary>\n\n```sql\n%s\n```\n\n</details>\n", strings.Count(body, "\n")+1, fenced(body))
	return b.String()
}

// sentence turns a file name back into the sentence it was named with.
func sentence(name string) string {
	s := strings.ReplaceAll(name, "_", " ")
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var (
	ruleLine   = regexp.MustCompile(`^[\s═─=\-#]*$`)
	bannerLine = regexp.MustCompile(`^[═─]{2,}\s*(.+?)\s*[═─]*$`)
)

// commentHeader renders the comment block a file opens with. The headers here are prose with a house
// style — banner lines for sections, rules for emphasis, capitals for stress — so banners become
// headings, rules disappear, and more deeply indented lines keep their layout.
func commentHeader(source, marker string) string {
	var lines []string
	for i, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if i == 0 && strings.HasPrefix(trimmed, "#!") {
			continue
		}
		if !strings.HasPrefix(trimmed, marker) {
			break
		}
		text := strings.TrimPrefix(trimmed, marker)
		if strings.Contains(text, "Copyright ") || strings.Contains(text, "SPDX-License-Identifier") {
			continue
		}
		lines = append(lines, text)
	}
	return renderComment(lines)
}

var blankRun = regexp.MustCompile(`\n{3,}`)

func renderComment(lines []string) string {
	// The prose indent is the least indent of an ordinary line; a line indented further than that is
	// laid out on purpose (a table, a list of reasons) and keeps its layout in a block.
	prose := -1
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || ruleLine.MatchString(t) || bannerLine.MatchString(t) {
			continue
		}
		if n := indentOf(l); prose < 0 || n < prose {
			prose = n
		}
	}
	var b strings.Builder
	var block []string
	flush := func() {
		if len(block) == 0 {
			return
		}
		least := -1
		for _, l := range block {
			if n := indentOf(l); least < 0 || n < least {
				least = n
			}
		}
		b.WriteString("\n```text\n")
		for _, l := range block {
			b.WriteString(strings.TrimRight(l[least:], " ") + "\n")
		}
		b.WriteString("```\n\n")
		block = nil
	}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		switch {
		case trimmed == "":
			flush()
			b.WriteString("\n")
		case ruleLine.MatchString(trimmed):
			flush()
		case bannerLine.MatchString(trimmed):
			flush()
			fmt.Fprintf(&b, "\n### %s\n\n", headingCase(bannerLine.FindStringSubmatch(trimmed)[1]))
		case indentOf(l) >= prose+2:
			block = append(block, l)
		default:
			flush()
			b.WriteString(trimmed + "\n")
		}
	}
	flush()
	return strings.TrimSpace(blankRun.ReplaceAllString(b.String(), "\n\n")) + "\n"
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

// headingCase lowers a heading written in capitals for emphasis, which reads as shouting in a
// rendered page, and leaves one written in ordinary case alone.
func headingCase(s string) string {
	if strings.ToUpper(s) != s {
		return s
	}
	r := []rune(strings.ToLower(s))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// plain strips markdown structure, for the one-line summaries in tables.
func plain(md string) string {
	var out []string
	inBlock := false
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, " ")
}

var statementStart = regexp.MustCompile(`(?i)^\s*(CREATE|ALTER|DROP|GRANT|REVOKE|COMMENT\s+ON|INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE)\b`)

// statements lists what a migration does, one line per statement, reading only top-level SQL: the
// bodies of functions are the function's business and would otherwise read as migration steps.
func statements(sql string) []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range strings.Split(stripBodies(stripComments(sql)), ";") {
		st = strings.Join(strings.Fields(st), " ")
		if !statementStart.MatchString(st) {
			continue
		}
		st = strings.ReplaceAll(st, "{schema}.", "")
		for _, cut := range []string{" (", " AS ", " USING ", " FOR EACH ", " IS "} {
			if i := strings.Index(st, cut); i > 0 && !strings.HasPrefix(strings.ToUpper(st), "GRANT") && !strings.HasPrefix(strings.ToUpper(st), "REVOKE") {
				st = st[:i]
			}
		}
		if len(st) > 110 {
			st = st[:107] + "..."
		}
		if !seen[st] {
			seen[st] = true
			out = append(out, st)
		}
	}
	return out
}

func stripComments(sql string) string {
	var b strings.Builder
	for _, l := range strings.Split(sql, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

var dollarTag = regexp.MustCompile(`\$[A-Za-z_]*\$`)

// stripBodies removes dollar-quoted text, closing each on the tag that opened it.
func stripBodies(sql string) string {
	var b strings.Builder
	for {
		loc := dollarTag.FindStringIndex(sql)
		if loc == nil {
			b.WriteString(sql)
			return b.String()
		}
		tag := sql[loc[0]:loc[1]]
		b.WriteString(sql[:loc[0]])
		rest := sql[loc[1]:]
		end := strings.Index(rest, tag)
		if end < 0 {
			return b.String()
		}
		sql = rest[end+len(tag):]
	}
}
