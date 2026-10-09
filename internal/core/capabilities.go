package core

import "sort"

type Spec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	ReadOnly    bool           `json:"read_only"`
	Scope       string         `json:"scope"`
}

func obj(props map[string]any, req ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	r := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(req) > 0 {
		r["required"] = req
	}
	return r
}
func str(d string) map[string]any     { return map[string]any{"type": "string", "description": d} }
func integer(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
func boolean(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func fields(base map[string]any, more map[string]any) map[string]any {
	p := map[string]any{}
	for k, v := range base {
		p[k] = v
	}
	for k, v := range more {
		p[k] = v
	}
	return p
}

var postFields = map[string]any{"title": str("Post title, 1–200 characters"), "slug": str("URL slug: lowercase ASCII words separated by hyphens"), "markdown": str("Markdown content, maximum 1 MiB. Raw HTML is not allowed."), "excerpt": str("Short plain-text description"), "tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 20}, "category": str("Category name"), "cover": str("Uploaded /media/ image URL or empty"), "featured": boolean("Feature this post"), "idempotency_key": str("Unique retry key; reuse only with identical arguments")}
var ref = map[string]any{"id": str("Post ID")}
var actionFields = fields(ref, map[string]any{"expected_revision": integer("Current draft revision obtained by posts.get; stale writes fail"), "confirm": boolean("Explicit intent to change public visibility or move to trash"), "idempotency_key": str("Unique retry key")})
var settingsSchema = obj(map[string]any{"title": str("Site title"), "description": str("Site description"), "author": str("Author display name"), "base_url": str("Absolute public HTTP(S) base URL")}, "title", "description", "author", "base_url")
var Specs = map[string]Spec{}

func init() {
	add := func(n, d, scope string, ro bool, p map[string]any, req ...string) {
		Specs[n] = Spec{n, d, obj(p, req...), ro, scope}
	}
	add("system.capabilities", "Discover exact operation schemas, permissions, and safe automation workflow", "read", true, nil)
	add("system.info", "Get version, instance revision, storage, counts and honest AI availability", "read", true, nil)
	add("posts.list", "List current authoring drafts, including unpublished changes", "read", true, map[string]any{"status": str("Optional draft, changed, published, or trash"), "query": str("Case-insensitive title/content filter")})
	add("posts.get", "Read a draft and immutable content revision history", "read", true, ref, "id")
	add("posts.preview", "Render a specific private revision without publishing. Fetch before publish.", "read", true, fields(ref, map[string]any{"revision": integer("Optional historical revision; defaults current")}), "id")
	add("posts.create", "Create a private draft. Never publishes implicitly.", "draft", false, postFields, "title", "slug", "markdown")
	add("posts.update", "Replace draft fields using revision CAS; live published content stays unchanged", "draft", false, fields(postFields, map[string]any{"id": str("Post ID"), "expected_revision": integer("Current draft revision")}), "id", "expected_revision", "title", "slug", "markdown")
	add("posts.publish", "Publish the exact reviewed draft revision; requires publisher role and explicit confirmation", "publish", false, actionFields, "id", "expected_revision", "confirm")
	add("posts.unpublish", "Remove public snapshot while preserving private draft and history", "publish", false, actionFields, "id", "expected_revision", "confirm")
	add("posts.delete", "Move post to trash; retained in backup, no irreversible purge", "publish", false, actionFields, "id", "expected_revision", "confirm")
	add("posts.recover", "Recover a trashed post as a private draft, never republishes", "publish", false, actionFields, "id", "expected_revision", "confirm")
	add("posts.restore", "Copy a historical content revision to a NEW draft, preserving live publication", "draft", false, fields(ref, map[string]any{"revision": integer("Historical content revision"), "expected_revision": integer("Current draft revision"), "idempotency_key": str("Retry key")}), "id", "revision", "expected_revision")
	add("settings.get", "Read site settings and their concurrency revision", "read", true, nil)
	add("settings.update", "Update site settings with revision guard", "admin", false, map[string]any{"settings": settingsSchema, "expected_revision": integer("Settings revision"), "idempotency_key": str("Retry key")}, "settings", "expected_revision")
	add("media.list", "List image assets without private base64 bytes", "read", true, nil)
	add("media.upload", "Upload a sniffed PNG, JPEG, GIF or WebP image up to 5 MiB; SVG is rejected", "draft", false, map[string]any{"name": str("Filename, no paths"), "base64": str("Standard base64 image bytes"), "idempotency_key": str("Retry key")}, "name", "base64")
	add("audit.list", "Read sanitized operation audit history; never contains tokens or article bodies", "admin", true, nil)
	add("backup.export", "Export consistent logical snapshot including revisions and media, with checksum", "admin", true, nil)
	add("backup.restore", "Restore a checksum-validated backup into an EMPTY instance; pending schedules resume unless serve uses --pause-schedules", "admin", false, map[string]any{"backup": map[string]any{"type": "object", "description": "Exact backup.export data"}, "confirm": boolean("Explicit intent to restore"), "idempotency_key": str("Retry key")}, "backup", "confirm")
}
func Capabilities() map[string]any {
	ops := []Spec{}
	for _, v := range Specs {
		ops = append(ops, v)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return map[string]any{"version": Version, "operations": ops, "workflow": []string{"Discover schemas", "Read current revision", "Create or update private draft", "Preview exact revision", "Publish with expected_revision and confirm:true"}, "limits": map[string]any{"markdown_bytes": 1048576, "media_bytes": 5242880, "snapshot_bytes": 67108864}, "ai_generation": false}
}
