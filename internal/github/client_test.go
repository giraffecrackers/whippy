package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("tok")
	c.BaseURL = srv.URL
	return c
}

// The answer a fine-grained token gets: data, with null check runs and an
// error for each.
const partialAnswer = `{
  "data": {
    "viewer": {"login": "ada"},
    "open": {"nodes": [
      {"number": 7, "title": "Add thing", "url": "https://github.com/o/r/pull/7", "isDraft": false,
       "reviewDecision": "REVIEW_REQUIRED", "mergeable": "CONFLICTING", "updatedAt": "2026-11-10T01:00:00Z",
       "headRefName": "thing", "repository": {"nameWithOwner": "o/r"},
       "commits": {"nodes": [{"commit": {"oid": "abc", "statusCheckRollup": {"state": "FAILURE", "contexts": {"nodes": [null, null]}}}}]}},
      {"number": 8, "title": "Full detail", "url": "https://github.com/o/s/pull/8", "isDraft": true,
       "reviewDecision": null, "mergeable": "MERGEABLE", "updatedAt": "2026-11-10T01:00:00Z",
       "headRefName": "detail", "repository": {"nameWithOwner": "o/s"},
       "commits": {"nodes": [{"commit": {"oid": "def", "statusCheckRollup": {"state": "PENDING", "contexts": {"nodes": [
         {"__typename": "CheckRun", "name": "test", "status": "IN_PROGRESS", "conclusion": null, "detailsUrl": "https://ci/1"},
         {"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://ci/2"},
         {"__typename": "StatusContext", "context": "deploy/preview", "state": "SUCCESS", "targetUrl": "https://ci/3"}
       ]}}}}]}},
      {}
    ]},
    "recent": {"nodes": [
      {"number": 7, "state": "OPEN", "repository": {"nameWithOwner": "o/r"}},
      {"number": 5, "state": "MERGED", "repository": {"nameWithOwner": "o/t"}},
      {"number": 6, "state": "CLOSED", "repository": {"nameWithOwner": "o/t"}}
    ]}
  },
  "errors": [{"type": "FORBIDDEN", "message": "Resource not accessible by personal access token"}]
}`

func TestPullRequests(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/graphql" {
			t.Errorf("request %s %s, auth %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Write([]byte(partialAnswer))
	})
	got, err := c.PullRequests(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Login != "ada" || len(got.Open) != 2 {
		t.Fatalf("login %q, %d open", got.Login, len(got.Open))
	}
	a := got.Open[0]
	if a.Key() != "o/r#7" || a.Rollup != StatusFailure || a.HeadSHA != "abc" || a.Mergeable != MergeableConflicting || len(a.Checks) != 0 {
		t.Errorf("first: %+v", a)
	}
	b := got.Open[1]
	want := []Check{{"test", StatusPending, "https://ci/1"}, {"lint", StatusFailure, "https://ci/2"}, {"deploy/preview", StatusSuccess, "https://ci/3"}}
	if !b.Draft || b.Rollup != StatusPending || len(b.Checks) != len(want) {
		t.Fatalf("second: %+v", b)
	}
	for i := range want {
		if b.Checks[i] != want[i] {
			t.Errorf("check %d: %+v, want %+v", i, b.Checks[i], want[i])
		}
	}
	if got.Closed["o/t#5"] != PRMerged || got.Closed["o/t#6"] != PRClosed || len(got.Closed) != 2 {
		t.Errorf("closed: %v", got.Closed)
	}
	if len(got.RecentRepos) != 3 || got.RecentRepos[0] != "o/r" || got.RecentRepos[2] != "o/t" {
		t.Errorf("repos: %v", got.RecentRepos)
	}
}

func TestPullRequestsErrorsWithoutData(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": null, "errors": [{"type": "RATE_LIMITED", "message": "slow down"}]}`))
	})
	_, err := c.PullRequests(context.Background(), time.Now())
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Errorf("err %v, want a rate limit", err)
	}
}

const runsAnswer = `{"workflow_runs": [
  {"id": 1, "name": ".github/workflows/main.yml", "display_title": "Deploy", "head_branch": "main", "head_sha": "abc",
   "event": "push", "status": "in_progress", "conclusion": null, "html_url": "https://github.com/o/r/actions/runs/1",
   "run_number": 12, "created_at": "2026-11-10T01:00:00Z", "updated_at": "2026-11-10T01:01:00Z"},
  {"id": 2, "name": "CI", "display_title": "Fix", "head_branch": "fix", "head_sha": "def",
   "event": "pull_request", "status": "completed", "conclusion": "cancelled", "html_url": "https://github.com/o/r/actions/runs/2",
   "run_number": 11, "created_at": "2026-11-10T00:00:00Z", "updated_at": "2026-11-10T00:05:00Z"}
]}`

func TestRunsUsesETag(t *testing.T) {
	var requests, notModified int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/repos/o/r/actions/runs" || r.URL.Query().Get("actor") != "ada" || r.URL.Query().Get("created") != ">=2026-11-09" {
			t.Errorf("request %s", r.URL)
		}
		if r.Header.Get("If-None-Match") == `W/"v1"` {
			notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Write([]byte(runsAnswer))
	})
	since := time.Date(2026, 11, 9, 13, 0, 0, 0, time.UTC)
	for i := range 2 {
		// A later instant of the same day keeps the URL, and so the ETag.
		runs, err := c.Runs(context.Background(), "o/r", "ada", since.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 2 || runs[0].Workflow != "main.yml" || runs[0].Status != StatusPending ||
			runs[1].Status != StatusSkipped || !runs[1].Cancelled || runs[1].Repo != "o/r" {
			t.Fatalf("pass %d: %+v", i, runs)
		}
	}
	if requests != 2 || notModified != 1 {
		t.Errorf("%d requests, %d not modified", requests, notModified)
	}
}

func TestStatusErrors(t *testing.T) {
	reset := time.Now().Add(10 * time.Minute).Unix()
	tests := []struct {
		name   string
		status int
		header map[string]string
		check  func(error) bool
	}{
		{"unauthorized", 401, nil, func(err error) bool { return errors.Is(err, ErrUnauthorized) }},
		{"forbidden repo", 403, map[string]string{"X-RateLimit-Remaining": "4000"}, func(err error) bool { return errors.Is(err, ErrNoAccess) }},
		{"missing repo", 404, nil, func(err error) bool { return errors.Is(err, ErrNoAccess) }},
		{"primary limit", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset, 10)}, func(err error) bool {
			var rl *RateLimitError
			return errors.As(err, &rl) && rl.RetryAt.Unix() == reset
		}},
		{"secondary limit", 403, map[string]string{"Retry-After": "30"}, func(err error) bool {
			var rl *RateLimitError
			return errors.As(err, &rl) && time.Until(rl.RetryAt) > 20*time.Second
		}},
		{"server error", 502, nil, func(err error) bool {
			var rl *RateLimitError
			return err != nil && !errors.As(err, &rl) && !errors.Is(err, ErrNoAccess) && !errors.Is(err, ErrUnauthorized)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
			})
			if _, err := c.Runs(context.Background(), "o/r", "ada", time.Now()); !tt.check(err) {
				t.Errorf("err %v", err)
			}
		})
	}
}

func runJSON(id int, status, conclusion string) string {
	return fmt.Sprintf(`{"id": %d, "name": "CI", "head_branch": "b%d", "status": %q, "conclusion": %q,
		"created_at": "2026-11-10T00:%02d:00Z", "updated_at": "2026-11-10T00:%02d:00Z"}`, id, id, status, conclusion, id, id)
}

func TestRunsReadsOlderPagesOnceAndFollowsPending(t *testing.T) {
	pages := map[string][]string{
		"1": {runJSON(5, "completed", "success"), runJSON(4, "completed", "success")},
		"2": {runJSON(3, "completed", "failure"), runJSON(2, "in_progress", "")},
		"3": {runJSON(1, "completed", "success")},
	}
	etag := `"v1"`
	var asked []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/actions/runs/2":
			asked = append(asked, "run 2")
			w.Write([]byte(runJSON(2, "completed", "failure")))
			return
		case "/repos/o/r/actions/runs/6":
			asked = append(asked, "run 6")
			w.Write([]byte(runJSON(6, "queued", "")))
			return
		}
		page := r.URL.Query().Get("page")
		if page == "1" && r.Header.Get("If-None-Match") == etag {
			asked = append(asked, "page 1 unchanged")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		asked = append(asked, "page "+page)
		if page == "1" {
			w.Header().Set("ETag", etag)
		}
		w.Write([]byte(`{"workflow_runs": [` + strings.Join(pages[page], ",") + `]}`))
	})
	c.perPage = 2
	since := time.Date(2026, 11, 9, 13, 0, 0, 0, time.UTC)
	ids := func() (ids []int64, statuses []Status) {
		t.Helper()
		runs, err := c.Runs(context.Background(), "o/r", "ada", since)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range runs {
			ids, statuses = append(ids, r.ID), append(statuses, r.Status)
		}
		return ids, statuses
	}
	expect := func(why string, want ...string) {
		t.Helper()
		if !slices.Equal(asked, want) {
			t.Errorf("%s: asked %q, want %q", why, asked, want)
		}
		asked = nil
	}

	// The first read goes through every page.
	got, statuses := ids()
	if !slices.Equal(got, []int64{5, 4, 3, 2, 1}) || statuses[3] != StatusPending {
		t.Fatalf("runs %v %v", got, statuses)
	}
	expect("first read", "page 1", "page 2", "page 3")

	// Run 2 is past the first page and was under way: asked for by itself.
	got, statuses = ids()
	if !slices.Equal(got, []int64{5, 4, 3, 2, 1}) || statuses[3] != StatusFailure {
		t.Fatalf("runs %v %v", got, statuses)
	}
	expect("following a run", "page 1 unchanged", "run 2")

	// Nothing under way and nothing new: one free request.
	ids()
	expect("unchanged", "page 1 unchanged")

	// One new run: the first page alone, as it reaches runs already known.
	etag = `"v2"`
	pages["1"] = []string{runJSON(6, "queued", ""), runJSON(5, "completed", "success")}
	got, _ = ids()
	if !slices.Equal(got, []int64{6, 5, 4, 3, 2, 1}) {
		t.Errorf("runs %v", got)
	}
	expect("one new run", "page 1")

	// Run 6 moves off the first page still pending, behind a burst of new
	// runs: the pages are read as far as the burst goes, and run 6 is
	// asked for by itself from then on.
	etag = `"v3"`
	pages["1"] = []string{runJSON(9, "completed", "success"), runJSON(8, "completed", "success")}
	pages["2"] = []string{runJSON(7, "completed", "success"), runJSON(6, "queued", "")}
	got, _ = ids()
	if !slices.Equal(got, []int64{9, 8, 7, 6, 5, 4, 3, 2, 1}) {
		t.Errorf("runs %v", got)
	}
	expect("a burst", "page 1", "page 2")
	ids()
	expect("after the burst", "page 1 unchanged", "run 6")
}

func TestRunsDropsRunsBeforeTheWindow(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"workflow_runs": [` + runJSON(1, "completed", "success") + `]}`))
	})
	ctx := context.Background()
	if runs, err := c.Runs(ctx, "o/r", "ada", time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)); err != nil || len(runs) != 1 {
		t.Fatalf("runs %v, err %v", runs, err)
	}
	// A day later the run is out of the window, though the client knew it.
	if runs, err := c.Runs(ctx, "o/r", "ada", time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)); err != nil || len(runs) != 0 {
		t.Errorf("runs %v, err %v", runs, err)
	}
}
