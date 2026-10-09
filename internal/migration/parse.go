package migration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func normalizeDate(s string) (string, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05", "2006-01-02", "02 Jan 2006"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.UTC().Format(time.RFC3339Nano), nil
		}
	}
	return "", fmt.Errorf("invalid date; use RFC3339 or YYYY-MM-DD")
}

// Parse supports an explicitly limited flat YAML/TOML frontmatter grammar.
// No frontmatter produces a safe draft with a filename/H1-derived title and slug.
func Parse(sourcePath string, b []byte) (Document, error) {
	d := Document{SourcePath: sourcePath, Tags: []string{}, Draft: true, Warnings: []string{}}
	if e := validPath(sourcePath); e != nil {
		return d, e
	}
	if len(b) > MaxFileBytes {
		return d, fmt.Errorf("file exceeds size limit")
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return d, fmt.Errorf("file must be UTF-8 without NUL")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	values := map[string]any{}
	raw := map[string]string{}
	lines := strings.Split(s, "\n")
	first := strings.TrimSpace(lines[0])
	if first == "---" || first == "+++" {
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == first && len(lines[i]) > 0 && lines[i][0] != ' ' && lines[i][0] != '\t' {
				end = i
				break
			}
		}
		if end < 0 {
			return d, fmt.Errorf("unclosed frontmatter")
		}
		front := strings.Join(lines[1:end], "\n")
		if len(front) > 64<<10 {
			return d, fmt.Errorf("frontmatter exceeds 64 KiB")
		}
		var e error
		values, raw, e = parseFront(lines[1:end], first == "+++")
		if e != nil {
			return d, e
		}
		d.Markdown = strings.Join(lines[end+1:], "\n")
	} else {
		d.Markdown = s
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := values[k]
		getString := func() (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("%s must be text", k)
			}
			return s, nil
		}
		var e error
		switch k {
		case "title":
			d.Title, e = getString()
		case "slug":
			d.Slug, e = getString()
		case "description", "summary", "excerpt":
			text, err := getString()
			e = err
			if d.Excerpt != "" && d.Excerpt != text {
				e = fmt.Errorf("description/summary/excerpt disagree")
			} else {
				d.Excerpt = text
			}
		case "category":
			text, err := getString()
			e = err
			if d.Category != "" && d.Category != text {
				e = fmt.Errorf("category/categories disagree")
			} else {
				d.Category = text
			}
		case "categories":
			list, ok := v.([]string)
			if !ok || len(list) != 1 {
				e = fmt.Errorf("categories requires exactly one category")
			} else if d.Category != "" && d.Category != list[0] {
				e = fmt.Errorf("category/categories disagree")
			} else {
				d.Category = list[0]
			}
		case "cover":
			d.Cover, e = getString()
		case "featured":
			var ok bool
			d.Featured, ok = v.(bool)
			if !ok {
				e = fmt.Errorf("featured must be true or false")
			}
		case "draft":
			var ok bool
			d.Draft, ok = v.(bool)
			if !ok {
				e = fmt.Errorf("draft must be true or false")
			}
			if ok && !d.Draft {
				d.Warnings = append(d.Warnings, "draft:false is source metadata only; import always saves a private draft")
			}
		case "tags":
			list, ok := v.([]string)
			if !ok {
				e = fmt.Errorf("tags must be a text array")
			} else {
				d.Tags = uniqueStrings(list)
			}
		case "date", "publishdate", "publish_date":
			text, err := getString()
			if err != nil {
				e = err
				break
			}
			date, err := normalizeDate(text)
			if err != nil {
				e = err
				break
			}
			if k == "date" {
				d.Date = date
			} else if d.PublishDate != "" && d.PublishDate != date {
				e = fmt.Errorf("publishDate aliases disagree")
			} else {
				d.PublishDate = date
			}
			d.Warnings = append(d.Warnings, k+" retained as metadata; existing posts API cannot set historical timestamps or schedule publication")
		default:
			if d.Extra == nil {
				d.Extra = map[string]string{}
			}
			d.Extra[k] = raw[k]
			d.Warnings = append(d.Warnings, "unsupported metadata retained, not applied: "+k)
		}
		if e != nil {
			return d, e
		}
	}
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		d.Title = heading(d.Markdown)
		if d.Title == "" {
			d.Title = strings.TrimSuffix(path.Base(sourcePath), path.Ext(sourcePath))
		}
		d.Warnings = append(d.Warnings, "title inferred from first H1 or filename")
	}
	if d.Slug == "" {
		d.Slug = fallbackSlug(sourcePath)
		d.Warnings = append(d.Warnings, "slug inferred from filename; non-ASCII filename uses stable path hash")
	}
	if strings.Contains(d.Markdown, "![") {
		d.Warnings = append(d.Warnings, "attachment references preserved verbatim; no attachment copied, uploaded, rewritten, or remotely fetched")
	}
	if e := ValidateDocument(d); e != nil {
		return d, e
	}
	d.ContentHash = HashDocument(d)
	return d, nil
}

func uniqueStrings(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}
func heading(s string) string {
	fence := ""
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			marker := t[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence == "" && strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[2:]), "#"))
		}
	}
	return ""
}
func fallbackSlug(p string) string {
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	if base == "index" || base == "_index" {
		base = path.Base(path.Dir(p))
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	s := b.String()
	if s == "" {
		return "post-" + digest([]byte(p))[:12]
	}
	if len(s) > 160 {
		s = strings.TrimRight(s[:140], "-") + "-" + digest([]byte(p))[:12]
	}
	return s
}

// stripComment respects quoted # and rejects unfinished strings via scalar.
func stripComment(s string) string {
	quote := byte(0)
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote == '"' && escape {
				escape = false
				continue
			}
			if quote == '"' && c == '\\' {
				escape = true
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		} else if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func scalar(s string, toml bool) (any, error) {
	s = stripComment(s)
	if s == "" {
		return "", nil
	}
	if s[0] == '"' {
		var v string
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			return nil, fmt.Errorf("invalid double-quoted scalar")
		}
		return v, nil
	}
	if s[0] == '\'' {
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return nil, fmt.Errorf("unclosed single-quoted scalar")
		}
		inside := s[1 : len(s)-1]
		if !toml {
			for i := 0; i < len(inside); i++ {
				if inside[i] == '\'' {
					if i+1 >= len(inside) || inside[i+1] != '\'' {
						return nil, fmt.Errorf("single quotes inside YAML literals must be doubled")
					}
					i++
				}
			}
			inside = strings.ReplaceAll(inside, "''", "'")
		} else if strings.Contains(inside, "'") {
			return nil, fmt.Errorf("invalid TOML literal")
		}
		return inside, nil
	}
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if strings.ContainsAny(s, "\t\n") || strings.Contains(s, ": ") || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "|") || strings.HasPrefix(s, ">") || strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "!") || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return nil, fmt.Errorf("unsupported nested/alias/typed scalar")
	}
	if toml {
		if _, e := normalizeDate(s); e == nil {
			return s, nil
		}
		if _, e := strconv.ParseFloat(s, 64); e == nil {
			return s, nil
		}
		return nil, fmt.Errorf("TOML text must be quoted")
	}
	return s, nil
}

func array(s string, toml bool) ([]string, error) {
	s = stripComment(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("unclosed array")
	}
	parts := []string{}
	start := 1
	quote := byte(0)
	escape := false
	for i := 1; i < len(s)-1; i++ {
		c := s[i]
		if quote != 0 {
			if quote == '"' && escape {
				escape = false
				continue
			}
			if quote == '"' && c == '\\' {
				escape = true
				continue
			}
			if c == quote {
				if quote == '\'' && !toml && i+1 < len(s)-1 && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		} else if c == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		} else if c == '[' || c == ']' || c == '{' || c == '}' {
			return nil, fmt.Errorf("nested array or map unsupported")
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed array quote")
	}
	parts = append(parts, s[start:len(s)-1])
	out := []string{}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			if len(parts) == 1 || i == len(parts)-1 {
				continue
			}
			return nil, fmt.Errorf("empty array item")
		}
		v, e := scalar(p, toml)
		if e != nil {
			return nil, e
		}
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("array requires text items")
		}
		out = append(out, text)
	}
	return out, nil
}

func parseFront(lines []string, toml bool) (map[string]any, map[string]string, error) {
	values := map[string]any{}
	raw := map[string]string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return nil, nil, fmt.Errorf("line %d: nested metadata unsupported", i+2)
		}
		sep := ":"
		if toml {
			sep = "="
		}
		pos := strings.Index(line, sep)
		if pos < 1 {
			return nil, nil, fmt.Errorf("line %d: invalid metadata assignment", i+2)
		}
		key := strings.ToLower(strings.TrimSpace(line[:pos]))
		if len(key) > 128 || strings.ContainsAny(key, " .[]{}&*!\"'\\\t") {
			return nil, nil, fmt.Errorf("line %d: unsupported metadata key", i+2)
		}
		if _, ok := values[key]; ok {
			return nil, nil, fmt.Errorf("duplicate metadata key %s", key)
		}
		value := stripComment(line[pos+1:])
		literal := value
		var v any
		var e error
		if !toml && (value == "|" || value == "|-" || value == ">" || value == ">-") {
			block := []string{}
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) != "" && !strings.HasPrefix(next, " ") {
					break
				}
				i++
				literal += "\n" + next
				if next == "" {
					block = append(block, "")
					continue
				}
				if !strings.HasPrefix(next, "  ") {
					return nil, nil, fmt.Errorf("block scalar requires two-space indentation")
				}
				block = append(block, next[2:])
			}
			v = strings.Join(block, "\n")
			if strings.HasPrefix(value, ">") {
				v = strings.Join(block, " ")
			}
			if !strings.HasSuffix(value, "-") {
				v = v.(string) + "\n"
			}
		} else if !toml && value == "" && i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") {
			list := []string{}
			for i+1 < len(lines) {
				next := lines[i+1]
				trim := strings.TrimSpace(next)
				if !strings.HasPrefix(trim, "- ") {
					break
				}
				i++
				literal += "\n" + next
				item, err := scalar(strings.TrimSpace(trim[2:]), false)
				if err != nil {
					return nil, nil, err
				}
				text, ok := item.(string)
				if !ok {
					return nil, nil, fmt.Errorf("list requires text items")
				}
				list = append(list, text)
			}
			v = list
		} else if strings.HasPrefix(value, "[") {
			for !strings.HasSuffix(strings.TrimSpace(value), "]") && i+1 < len(lines) {
				i++
				part := stripComment(lines[i])
				value += " " + part
				literal += "\n" + lines[i]
			}
			v, e = array(value, toml)
		} else {
			v, e = scalar(value, toml)
		}
		if e != nil {
			return nil, nil, fmt.Errorf("metadata %s: %w", key, e)
		}
		values[key] = v
		raw[key] = literal
	}
	return values, raw, nil
}
