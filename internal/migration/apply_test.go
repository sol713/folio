package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeCaller struct {
	calls      []string
	cache      map[string]json.RawMessage
	posts      map[string]postRequest
	writes     int
	loseAfter  int
	failBefore int
	malformed  bool
	cancel     context.CancelFunc
}

func newFake() *fakeCaller {
	return &fakeCaller{cache: map[string]json.RawMessage{}, posts: map[string]postRequest{}}
}
func (f *fakeCaller) Call(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, op+":"+string(args))
	if op != "posts.create" && op != "posts.update" {
		panic("unexpected operation")
	}
	var req postRequest
	decoder := json.NewDecoder(strings.NewReader(string(args)))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&req); e != nil {
		return nil, e
	}
	if previous, ok := f.cache[req.IdempotencyKey]; ok {
		return previous, nil
	}
	if f.failBefore == len(f.calls) {
		return nil, errors.New("BEARER_PRIVATE_DO_NOT_LEAK")
	}
	f.writes++
	id := req.ID
	revision := req.ExpectedRevision + 1
	if op == "posts.create" {
		id = fmt.Sprintf("post-%d", f.writes)
		revision = 1
	}
	f.posts[id] = req
	b, _ := json.Marshal(map[string]any{"post": map[string]any{"id": id, "slug": req.Slug, "revision": revision}})
	f.cache[req.IdempotencyKey] = b
	if f.cancel != nil {
		f.cancel()
	}
	if f.loseAfter == f.writes {
		return nil, errors.New("BEARER_PRIVATE_DO_NOT_LEAK")
	}
	if f.malformed {
		return json.RawMessage(`{"post":{}}`), nil
	}
	return b, nil
}
func TestApplyCreatesPrivateDraftsOnly(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a"), doc(t, "b")}, nil, Options{})
	f := newFake()
	r, e := Apply(context.Background(), f, p, ApplyOptions{})
	if e != nil || !r.Complete || f.writes != 2 {
		t.Fatal(r, e)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "publish") || strings.Contains(call, "draft") {
			t.Fatal(call)
		}
	}
}
func TestLostResponseReplayDoesNotDuplicate(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a"), doc(t, "b"), doc(t, "c")}, nil, Options{})
	f := newFake()
	f.loseAfter = 2
	r, e := Apply(context.Background(), f, p, ApplyOptions{})
	if !errors.Is(e, ErrPartialApply) || r.Complete || f.writes != 2 || r.Outcomes[0].Status != "applied" || r.Outcomes[1].Status != "failed" || r.Outcomes[2].Status != "pending" {
		t.Fatal(r, e)
	}
	firstCalls := append([]string(nil), f.calls...)
	f.loseAfter = 0
	retry, e := Apply(context.Background(), f, p, ApplyOptions{})
	if e != nil || !retry.Complete || f.writes != 3 || len(f.posts) != 3 {
		t.Fatal(retry, e)
	}
	if f.calls[2] != firstCalls[0] || f.calls[3] != firstCalls[1] {
		t.Fatal("retry changed request bytes")
	}
}
func TestApplyErrorRedactionAndContinue(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a"), doc(t, "b")}, nil, Options{})
	f := newFake()
	f.failBefore = 1
	r, e := Apply(context.Background(), f, p, ApplyOptions{ContinueOnError: true})
	b, _ := json.Marshal(r)
	if e == nil || f.writes != 1 || r.Outcomes[1].Status != "applied" || strings.Contains(string(b), "PRIVATE") || strings.Contains(e.Error(), "PRIVATE") {
		t.Fatal(r, e)
	}
}
func TestApplyUpdateCASAndSkip(t *testing.T) {
	d := doc(t, "a")
	existing := []Existing{{"existing-id", "a", 9}}
	for _, policy := range []string{"update", "skip"} {
		t.Run(policy, func(t *testing.T) {
			p := planOK(t, []Document{d}, existing, Options{Conflict: policy})
			f := newFake()
			r, e := Apply(context.Background(), f, p, ApplyOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if policy == "skip" {
				if len(f.calls) != 0 || r.Outcomes[0].Status != "skipped" {
					t.Fatal(r)
				}
			} else {
				if !strings.HasPrefix(f.calls[0], "posts.update:") || r.Outcomes[0].PostID != "existing-id" || r.Outcomes[0].Revision != 10 {
					t.Fatal(r)
				}
			}
		})
	}
}
func TestInvalidPlanNeverCallsAdapter(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a")}, []Existing{{"id", "a", 1}}, Options{})
	f := newFake()
	if _, e := Apply(context.Background(), f, p, ApplyOptions{}); e == nil || len(f.calls) > 0 {
		t.Fatal("blocked plan wrote")
	}
}
func TestApplyCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := planOK(t, []Document{doc(t, "a"), doc(t, "b")}, nil, Options{})
	f := newFake()
	f.cancel = cancel
	r, e := Apply(ctx, f, p, ApplyOptions{})
	if e == nil || len(f.calls) != 1 || r.Outcomes[1].Status != "failed" {
		t.Fatal(r, e)
	}
}
func TestMalformedReceiptMayCommitAndIsRecoverable(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a")}, nil, Options{})
	f := newFake()
	f.malformed = true
	r, e := Apply(context.Background(), f, p, ApplyOptions{})
	if e == nil || !strings.Contains(r.Outcomes[0].Error, "may have committed") || f.writes != 1 {
		t.Fatal(r, e)
	}
	f.malformed = false
	r, e = Apply(context.Background(), f, p, ApplyOptions{})
	if e != nil || f.writes != 1 || !r.Complete {
		t.Fatal(r, e)
	}
}
func TestNilAdapterRejected(t *testing.T) {
	p := planOK(t, nil, nil, Options{})
	if _, e := Apply(context.Background(), nil, p, ApplyOptions{}); e == nil {
		t.Fatal("nil adapter accepted")
	}
}
