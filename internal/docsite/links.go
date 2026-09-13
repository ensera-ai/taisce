// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The written documents link the way GitHub renders them: relative paths into the repository. On the
// site those have to land somewhere a reader can follow, so each one is resolved here, once:
//
//   - a document that is on the site stays a relative link to its page;
//   - a Go package directory becomes a link to that package's reference page, which is what a
//     reader following "internal/formation/" wants and what GitHub's directory listing is not;
//   - a file under site/static/ becomes the path the renderer serves it at, so a screenshot a
//     document shows is an image on the site as it is on GitHub. A link to its source page would be
//     an HTML page, which a browser does not display where an image belongs;
//   - anything else in the repository becomes a link to the source at the ref the site was built from;
//   - a path that names nothing is refused, because the reader would find nothing either.
//
// Only the path is checked here. Anchors are checked by the renderer against the pages it rendered,
// because its heading identifiers are the ground truth and a second implementation of them would be
// one more thing that could disagree.

var (
	inlineLink    = regexp.MustCompile(`\]\(([^)\s]+)((?:\s+"[^"]*")?)\)`)
	referenceLink = regexp.MustCompile(`(?m)^(\s{0,3}\[[^\]]+\]:\s*)(\S+)`)
	scheme        = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	fence         = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	backtickRun   = regexp.MustCompile("`+")
)

// staticDir is the renderer's static directory. Its files are served from the site's root: the
// renderer places site/static/img/x.png at /img/x.png and resolves such a path against the base URL.
const staticDir = "site/static/"

// resolveLinks rewrites every page's links in place and reports every broken one at once, so a run
// names the whole list rather than the first.
func resolveLinks(pages []page, opts Options, packages []goPackage) error {
	targets := map[string]string{}
	for _, p := range pages {
		if p.Repo != "" && p.Stage != "SUMMARY.md" {
			targets[p.Repo] = p.Stage
		}
	}
	for _, p := range packages {
		targets[p.Dir] = packagePage(p.Dir)
	}
	var broken []string
	for i := range pages {
		body, bad := rewriteLinks(pages[i], targets, opts)
		pages[i].Body = body
		broken = append(broken, bad...)
	}
	if len(broken) > 0 {
		sort.Strings(broken)
		return fmt.Errorf("docsite: %d link(s) name nothing in the repository:\n  %s", len(broken), strings.Join(broken, "\n  "))
	}
	return nil
}

// rewriteLinks rewrites one page. Fenced code and code spans are left alone: text in them is an
// example, not a link.
func rewriteLinks(p page, targets map[string]string, opts Options) (string, []string) {
	var broken []string
	resolve := func(target string) string {
		out, err := resolveTarget(p, target, targets, opts)
		if err != nil {
			broken = append(broken, fmt.Sprintf("%s: %s", pageName(p), err))
			return target
		}
		return out
	}
	lines := strings.Split(p.Body, "\n")
	inFence := false
	// A code span may wrap onto the next line of its paragraph, so whether a line starts inside one
	// is carried from the line before; a blank line ends the paragraph and any span with it.
	openSpan := ""
	for i, line := range lines {
		if fence.MatchString(line) {
			inFence = !inFence
			openSpan = ""
			continue
		}
		if inFence {
			continue
		}
		if strings.TrimSpace(line) == "" {
			openSpan = ""
			continue
		}
		lines[i] = outsideCode(line, &openSpan, func(text string) string {
			text = inlineLink.ReplaceAllStringFunc(text, func(m string) string {
				parts := inlineLink.FindStringSubmatch(m)
				return "](" + resolve(parts[1]) + parts[2] + ")"
			})
			return referenceLink.ReplaceAllStringFunc(text, func(m string) string {
				parts := referenceLink.FindStringSubmatch(m)
				return parts[1] + resolve(parts[2])
			})
		})
	}
	return strings.Join(lines, "\n"), broken
}

// outsideCode applies fn to the parts of a line that are not inside a code span. A span opens with a
// run of backticks and closes with a run of the same length; open carries an unclosed opener from
// one line of a paragraph to the next.
func outsideCode(line string, open *string, fn func(string) string) string {
	var b strings.Builder
	last := 0
	for _, run := range backtickRun.FindAllStringIndex(line, -1) {
		ticks := line[run[0]:run[1]]
		switch {
		case *open == "":
			b.WriteString(fn(line[last:run[0]]))
			b.WriteString(ticks)
			*open = ticks
		case ticks == *open:
			b.WriteString(line[last:run[1]])
			*open = ""
		default:
			b.WriteString(line[last:run[1]])
		}
		last = run[1]
	}
	if *open == "" {
		b.WriteString(fn(line[last:]))
	} else {
		b.WriteString(line[last:])
	}
	return b.String()
}

func resolveTarget(p page, target string, targets map[string]string, opts Options) (string, error) {
	if scheme.MatchString(target) || strings.HasPrefix(target, "#") {
		return target, nil
	}
	if p.Repo == "" {
		// A generated page already links to staged paths.
		return target, nil
	}
	file, fragment, _ := strings.Cut(target, "#")
	if fragment != "" {
		fragment = "#" + fragment
	}
	unescaped, err := url.PathUnescape(file)
	if err != nil {
		return "", fmt.Errorf("%s is not a valid path", target)
	}
	resolved := path.Clean(path.Join(path.Dir(p.Repo), unescaped))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("%s leaves the repository", target)
	}
	if stage, ok := targets[resolved]; ok {
		return relative(p.Stage, stage) + fragment, nil
	}
	info, err := os.Stat(filepath.Join(opts.Root, filepath.FromSlash(resolved)))
	if err != nil {
		return "", fmt.Errorf("%s names nothing", target)
	}
	if served, ok := strings.CutPrefix(resolved, staticDir); ok && !info.IsDir() {
		return "/" + served + fragment, nil
	}
	kind := "blob"
	if info.IsDir() {
		kind = "tree"
	}
	return sourceURL(opts, kind, resolved) + fragment, nil
}

func sourceURL(opts Options, kind, repoPath string) string {
	return fmt.Sprintf("https://github.com/%s/%s/%s/%s", opts.Repo, kind, opts.Ref, repoPath)
}

func pageName(p page) string {
	if p.Repo != "" {
		return p.Repo
	}
	return p.Stage
}
