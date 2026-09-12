// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The navigation is written once, as docs/SUMMARY.md: parts as headings, pages as nested list items.
// That file reads as a table of contents on GitHub and is the one a person edits. The renderer wants
// a sidebar definition instead, so it is derived here rather than kept as a second copy that would
// drift from the first.
//
// Two sidebars come out of it. The part named "Reference" becomes a sidebar of its own, because the
// generated reference is several hundred pages and would bury the written documents if they shared
// one. Every other part becomes a category of the guide. A page with pages nested under it becomes a
// category that is itself a link to that page.

type sidebarItem struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	Label     string         `json:"label,omitempty"`
	Link      *sidebarLink   `json:"link,omitempty"`
	Collapsed *bool          `json:"collapsed,omitempty"`
	Items     []*sidebarItem `json:"items,omitempty"`
}

type sidebarLink struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

var (
	partLine   = regexp.MustCompile(`^#\s+(.+?)\s*$`)
	entryLine  = regexp.MustCompile(`^( *)- \[([^\]]+)\]\(([^)\s]+)\)\s*$`)
	prefixLine = regexp.MustCompile(`^\[([^\]]+)\]\(([^)\s]+)\)\s*$`)
)

const referencePart = "Reference"

func sidebars(summary string) ([]byte, error) {
	bars := map[string][]*sidebarItem{"guide": {}, "reference": {}}
	current := "guide"
	var part *sidebarItem
	var stack []*sidebarItem
	add := func(item *sidebarItem) {
		if part != nil {
			part.Items = append(part.Items, item)
		} else {
			bars[current] = append(bars[current], item)
		}
	}
	for n, line := range strings.Split(summary, "\n") {
		switch {
		case partLine.MatchString(line):
			title := partLine.FindStringSubmatch(line)[1]
			stack = nil
			switch title {
			case "Summary":
				part = nil
			case referencePart:
				current, part = "reference", nil
			default:
				part = &sidebarItem{Type: "category", Label: title, Collapsed: boolPtr(false)}
				bars[current] = append(bars[current], part)
			}
		case prefixLine.MatchString(line):
			m := prefixLine.FindStringSubmatch(line)
			item := &sidebarItem{Type: "doc", ID: docID(m[2]), Label: m[1]}
			add(item)
			stack = []*sidebarItem{item}
		case entryLine.MatchString(line):
			m := entryLine.FindStringSubmatch(line)
			depth := len(m[1]) / 2
			item := &sidebarItem{Type: "doc", ID: docID(m[3]), Label: m[2]}
			if depth == 0 {
				add(item)
				stack = []*sidebarItem{item}
				continue
			}
			if depth > len(stack) {
				return nil, fmt.Errorf("docsite: docs/SUMMARY.md line %d is nested deeper than the entry above it", n+1)
			}
			parent := stack[depth-1]
			if parent.Type == "doc" {
				parent.Type, parent.Link, parent.ID, parent.Collapsed = "category", &sidebarLink{Type: "doc", ID: parent.ID}, "", boolPtr(true)
			}
			parent.Items = append(parent.Items, item)
			stack = append(stack[:depth], item)
		}
	}
	return json.MarshalIndent(bars, "", "  ")
}

func docID(target string) string { return strings.TrimSuffix(target, ".md") }

func boolPtr(b bool) *bool { return &b }
