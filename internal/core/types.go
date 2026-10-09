package core

import (
	"encoding/json"
	"fmt"
	"time"
)

const Version = "0.2.0"

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Template  string `json:"-"`
	Arguments []any  `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func Err(code, format string, a ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...), Template: format, Arguments: a}
}

type Post struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Slug              string   `json:"slug"`
	Excerpt           string   `json:"excerpt"`
	Markdown          string   `json:"markdown"`
	HTML              string   `json:"html"`
	Tags              []string `json:"tags"`
	Category          string   `json:"category"`
	Cover             string   `json:"cover"`
	Featured          bool     `json:"featured"`
	Status            string   `json:"status"`
	Revision          int      `json:"revision"`
	PublishedRevision int      `json:"published_revision"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	PublishedAt       string   `json:"published_at"`
}
type Record struct {
	Draft     Post     `json:"draft"`
	Live      *Post    `json:"live,omitempty"`
	Revisions []Post   `json:"revisions"`
	Deleted   bool     `json:"deleted"`
	OldSlugs  []string `json:"old_slugs"`
}
type Settings struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Author      string `json:"author"`
	BaseURL     string `json:"base_url"`
}
type Media struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	MIME      string `json:"mime"`
	Size      int    `json:"size"`
	CreatedAt string `json:"created_at"`
	Data      string `json:"base64,omitempty"`
	SHA256    string `json:"sha256"`
}
type Event struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Actor     string `json:"actor"`
	At        string `json:"at"`
	Revision  int    `json:"revision"`
}
type Idempotent struct {
	Fingerprint string          `json:"fingerprint"`
	Result      json.RawMessage `json:"result"`
}
type State struct {
	Schema           int                     `json:"schema"`
	Revision         int                     `json:"revision"`
	Settings         Settings                `json:"settings"`
	SettingsRevision int                     `json:"settings_revision"`
	Posts            map[string]*Record      `json:"posts"`
	Media            map[string]Media        `json:"media"`
	Audit            []Event                 `json:"audit"`
	Idempotency      map[string]Idempotent   `json:"idempotency"`
	Proposals        map[string]Proposal     `json:"proposals,omitempty"`
	Schedules        map[string]Schedule     `json:"schedules,omitempty"`
	ImportSources    map[string]ImportSource `json:"import_sources,omitempty"`
}
type Backup struct {
	Format    string          `json:"format"`
	Version   int             `json:"version"`
	CreatedAt string          `json:"created_at"`
	SHA256    string          `json:"sha256"`
	State     json.RawMessage `json:"state"`
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }
func freshState() State {
	return State{Schema: SchemaVersion, Revision: 1, Settings: Settings{"FOLIO", "A journal of considered ideas, small discoveries, and life in progress.", "Alex Morgan", "http://localhost:8080"}, SettingsRevision: 1, Posts: map[string]*Record{}, Media: map[string]Media{}, Idempotency: map[string]Idempotent{}, Audit: []Event{}}
}
