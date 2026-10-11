package core

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// Search describes the current saved draft, never history or rendered HTML.
// Rune offsets refer to the original source; clients must verify its revision.
type StoryMatch struct {
	Field          string `json:"field"`
	Line           int    `json:"line"`
	Start          int    `json:"start_rune"`
	End            int    `json:"end_rune"`
	Text           string `json:"text"`
	Snippet        string `json:"snippet"`
	SnippetStart   int    `json:"snippet_start_rune"`
	TruncatedStart bool   `json:"truncated_before"`
	TruncatedEnd   bool   `json:"truncated_after"`
}

type StoryHit struct {
	ID                string       `json:"id"`
	Title             string       `json:"title"`
	Slug              string       `json:"slug"`
	Status            string       `json:"status"`
	Revision          int          `json:"revision"`
	PublishedRevision int          `json:"published_revision"`
	UpdatedAt         string       `json:"updated_at"`
	Matches           []StoryMatch `json:"matches"`
}

type StorySearch struct {
	Query            string     `json:"query"`
	Status           string     `json:"status"`
	Limit            int        `json:"limit"`
	Total            int        `json:"total"`
	Truncated        bool       `json:"truncated"`
	InstanceID       string     `json:"instance_id"`
	InstanceRevision int        `json:"instance_revision"`
	Results          []StoryHit `json:"results"`
	Limits           []string   `json:"limitations"`
}

func (s *Store) searchStories(raw []byte) (any, error) {
	var a struct {
		Query  string          `json:"query"`
		Status json.RawMessage `json:"status"`
		Limit  json.RawMessage `json:"limit"`
	}
	if e := decode(raw, &a); e != nil {
		return nil, e
	}
	query, status, limit := strings.TrimSpace(a.Query), "", 20
	if query == "" || utf8.RuneCountInString(a.Query) > 128 {
		return nil, Err("validation", "Story search requires a 1–128 character query, a valid status, and a limit from 1 to 50")
	}
	if len(a.Status) > 0 && (string(a.Status) == "null" || json.Unmarshal(a.Status, &status) != nil) {
		return nil, Err("validation", "Story search requires a 1–128 character query, a valid status, and a limit from 1 to 50")
	}
	if len(a.Limit) > 0 && (string(a.Limit) == "null" || json.Unmarshal(a.Limit, &limit) != nil) {
		return nil, Err("validation", "Story search requires a 1–128 character query, a valid status, and a limit from 1 to 50")
	}
	validStatus := status == "" || status == "draft" || status == "changed" || status == "published" || status == "trash"
	if !validStatus || limit < 1 || limit > 50 {
		return nil, Err("validation", "Story search requires a 1–128 character query, a valid status, and a limit from 1 to 50")
	}
	out := StorySearch{Query: query, Status: status, Limit: limit, InstanceID: s.instanceID, InstanceRevision: s.State.Revision,
		Results: []StoryHit{}, Limits: []string{"current_saved_drafts_only", "private_read_scope", "unicode_simple_case_literal_substring", "first_match_per_field", "title_excerpt_markdown_slug_tags_priority", "original_source_rune_offsets", "point_in_time_catalog", "refine_query_if_truncated", "no_network_or_writes"}}
	needle := strings.ToLower(query)
	ranked := []struct {
		hit      StoryHit
		priority int
	}{}
	for _, record := range s.State.Posts {
		p := record.Draft
		if record.Deleted && status != "trash" || !record.Deleted && status == "trash" || status != "" && p.Status != status {
			continue
		}
		hit := StoryHit{ID: p.ID, Title: p.Title, Slug: p.Slug, Status: p.Status, Revision: p.Revision, PublishedRevision: p.PublishedRevision, UpdatedAt: p.UpdatedAt, Matches: []StoryMatch{}}
		priority := 5
		// Slug/tags preserve the existing Studio search options. Their source
		// strings are exactly the corresponding editor input values.
		for i, field := range []struct{ name, text string }{{"title", p.Title}, {"excerpt", p.Excerpt}, {"markdown", p.Markdown}, {"slug", p.Slug}, {"tags", strings.Join(p.Tags, ", ")}} {
			if match, ok := storyMatch(field.name, field.text, needle); ok {
				if i < priority {
					priority = i
				}
				hit.Matches = append(hit.Matches, match)
			}
		}
		if len(hit.Matches) > 0 {
			out.Total++
			ranked = append(ranked, struct {
				hit      StoryHit
				priority int
			}{hit, priority})
			// Keep at most limit candidates, not snippets for every catalog hit.
			sort.Slice(ranked, func(i, j int) bool {
				a, b := ranked[i], ranked[j]
				if a.priority != b.priority {
					return a.priority < b.priority
				}
				if a.hit.UpdatedAt != b.hit.UpdatedAt {
					return a.hit.UpdatedAt > b.hit.UpdatedAt
				}
				return a.hit.ID < b.hit.ID
			})
			if len(ranked) > limit {
				ranked = ranked[:limit]
			}
		}
	}
	out.Truncated = out.Total > limit
	for i := 0; i < len(ranked) && i < limit; i++ {
		out.Results = append(out.Results, ranked[i].hit)
	}
	return out, nil
}

func storyMatch(field, source, needle string) (StoryMatch, bool) {
	folded := strings.ToLower(source)
	index := strings.Index(folded, needle)
	if index < 0 {
		return StoryMatch{}, false
	}
	// Unicode simple lowercasing preserves rune count, not byte count (İ → i).
	start, width := utf8.RuneCountInString(folded[:index]), utf8.RuneCountInString(needle)
	runes := []rune(source)
	end := start + width
	left, right := max(0, start-40), min(len(runes), end+60)
	return StoryMatch{Field: field, Line: strings.Count(string(runes[:start]), "\n") + 1, Start: start, End: end,
		Text: string(runes[start:end]), Snippet: string(runes[left:right]), SnippetStart: left, TruncatedStart: left > 0, TruncatedEnd: right < len(runes)}, true
}
