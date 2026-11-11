// Package github reads the viewer's pull requests and workflow runs.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	// ErrUnauthorized means GitHub rejected the token.
	ErrUnauthorized = errors.New("github: token rejected")
	// ErrNoAccess means the token may not read this one resource.
	ErrNoAccess = errors.New("github: resource not accessible")
)

// RateLimitError says when GitHub will take requests again.
type RateLimitError struct{ RetryAt time.Time }

func (e *RateLimitError) Error() string {
	return "github: rate limited until " + e.RetryAt.Format(time.Kitchen)
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	token   string
	now     func() time.Time

	perPage int

	mu   sync.Mutex
	runs map[string]*repoRuns // by repo
}

const (
	defaultRunsPerPage = 30
	// maxRunPages bounds the first read of a busy repository's runs.
	maxRunPages = 5
	// maxFollowedRuns bounds the runs asked for one by one in a poll.
	maxFollowedRuns = 10
)

// repoRuns is what the client knows of a repository's runs between polls.
type repoRuns struct {
	// url and etag are those of the newest page's last answer.
	url, etag string
	runs      map[int64]Run
	// firstPage holds the runs that answer listed.
	firstPage  map[int64]bool
	backfilled bool
}

func New(token string) *Client {
	return &Client{
		BaseURL: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		token:   token,
		now:     time.Now,
		perPage: defaultRunsPerPage,
		runs:    map[string]*repoRuns{},
	}
}

func (c *Client) do(ctx context.Context, method, url, etag string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "whippy")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if err := c.statusError(resp); err != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func (c *Client) statusError(resp *http.Response) error {
	switch {
	case resp.StatusCode < 300 || resp.StatusCode == http.StatusNotModified:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		// A 403 is a rate limit only when GitHub says when to come back.
		if s := resp.Header.Get("Retry-After"); s != "" {
			if secs, err := strconv.Atoi(s); err == nil {
				return &RateLimitError{RetryAt: c.now().Add(time.Duration(secs) * time.Second)}
			}
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if unix, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
				return &RateLimitError{RetryAt: time.Unix(unix, 0)}
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return &RateLimitError{RetryAt: c.now().Add(time.Minute)}
		}
		return ErrNoAccess
	case resp.StatusCode == http.StatusNotFound:
		return ErrNoAccess
	default:
		return fmt.Errorf("github: %s", resp.Status)
	}
}

const pullRequestsQuery = `query($open: String!, $recent: String!) {
  viewer { login }
  open: search(query: $open, type: ISSUE, first: 50) {
    nodes { ... on PullRequest {
      number title url isDraft reviewDecision mergeable updatedAt headRefName
      repository { nameWithOwner }
      commits(last: 1) { nodes { commit { oid statusCheckRollup {
        state
        contexts(first: 100) { nodes {
          __typename
          ... on CheckRun { name status conclusion detailsUrl }
          ... on StatusContext { context state targetUrl }
        } }
      } } } }
    } }
  }
  recent: search(query: $recent, type: ISSUE, first: 50) {
    nodes { ... on PullRequest { number state repository { nameWithOwner } } }
  }
}`

type gqlRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type gqlResponse struct {
	Data *struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Open struct {
			Nodes []struct {
				Number         int       `json:"number"`
				Title          string    `json:"title"`
				URL            string    `json:"url"`
				IsDraft        bool      `json:"isDraft"`
				ReviewDecision string    `json:"reviewDecision"`
				Mergeable      string    `json:"mergeable"`
				UpdatedAt      time.Time `json:"updatedAt"`
				HeadRefName    string    `json:"headRefName"`
				Repository     gqlRepo   `json:"repository"`
				Commits        struct {
					Nodes []struct {
						Commit struct {
							OID    string `json:"oid"`
							Rollup *struct {
								State    string `json:"state"`
								Contexts struct {
									// A node is null where the token may
									// not read it.
									Nodes []*struct {
										Typename   string `json:"__typename"`
										Name       string `json:"name"`
										Status     string `json:"status"`
										Conclusion string `json:"conclusion"`
										DetailsURL string `json:"detailsUrl"`
										Context    string `json:"context"`
										State      string `json:"state"`
										TargetURL  string `json:"targetUrl"`
									} `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"nodes"`
		} `json:"open"`
		Recent struct {
			Nodes []struct {
				Number     int     `json:"number"`
				State      string  `json:"state"`
				Repository gqlRepo `json:"repository"`
			} `json:"nodes"`
		} `json:"recent"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// PullRequests returns the viewer's open pull requests, and what became of
// those updated since the given time.
func (c *Client) PullRequests(ctx context.Context, recentSince time.Time) (PullRequests, error) {
	body, err := json.Marshal(map[string]any{
		"query": pullRequestsQuery,
		"variables": map[string]string{
			"open":   "is:pr is:open author:@me archived:false sort:updated-desc",
			"recent": "is:pr author:@me sort:updated-desc updated:>=" + recentSince.UTC().Format("2006-01-02"),
		},
	})
	if err != nil {
		return PullRequests{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, c.BaseURL+"/graphql", "", body)
	if err != nil {
		return PullRequests{}, err
	}
	defer resp.Body.Close()
	var r gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return PullRequests{}, fmt.Errorf("github: reading pull requests: %w", err)
	}
	// GitHub answers with what the token may read and an error for each
	// field it may not, so errors beside data are not a failure.
	if r.Data == nil {
		for _, e := range r.Errors {
			if e.Type == "RATE_LIMITED" {
				return PullRequests{}, &RateLimitError{RetryAt: c.now().Add(time.Minute)}
			}
		}
		if len(r.Errors) > 0 {
			return PullRequests{}, errors.New("github: " + r.Errors[0].Message)
		}
		return PullRequests{}, errors.New("github: empty answer")
	}

	out := PullRequests{Login: r.Data.Viewer.Login, Closed: map[string]string{}}
	repos := map[string]bool{}
	for _, n := range r.Data.Open.Nodes {
		if n.Repository.NameWithOwner == "" {
			continue // not a pull request, or not readable
		}
		pr := PR{
			Repo:      n.Repository.NameWithOwner,
			Number:    n.Number,
			Title:     n.Title,
			URL:       n.URL,
			Branch:    n.HeadRefName,
			Draft:     n.IsDraft,
			Review:    n.ReviewDecision,
			Mergeable: n.Mergeable,
			UpdatedAt: n.UpdatedAt,
		}
		if len(n.Commits.Nodes) > 0 {
			commit := n.Commits.Nodes[0].Commit
			pr.HeadSHA = commit.OID
			if commit.Rollup != nil {
				pr.Rollup = commitStatus(commit.Rollup.State)
				for _, ctx := range commit.Rollup.Contexts.Nodes {
					switch {
					case ctx == nil:
					case ctx.Typename == "CheckRun":
						pr.Checks = append(pr.Checks, Check{ctx.Name, checkRunStatus(ctx.Status, ctx.Conclusion), ctx.DetailsURL})
					case ctx.Typename == "StatusContext":
						pr.Checks = append(pr.Checks, Check{ctx.Context, commitStatus(ctx.State), ctx.TargetURL})
					}
				}
			}
		}
		out.Open = append(out.Open, pr)
		repos[pr.Repo] = true
	}
	for _, n := range r.Data.Recent.Nodes {
		if n.Repository.NameWithOwner == "" {
			continue
		}
		repos[n.Repository.NameWithOwner] = true
		if n.State == PRMerged || n.State == PRClosed {
			out.Closed[PR{Repo: n.Repository.NameWithOwner, Number: n.Number}.Key()] = n.State
		}
	}
	for repo := range repos {
		out.RecentRepos = append(out.RecentRepos, repo)
	}
	sort.Strings(out.RecentRepos)
	return out, nil
}

type apiRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	DisplayTitle string    `json:"display_title"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	HTMLURL      string    `json:"html_url"`
	RunNumber    int       `json:"run_number"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	RunStartedAt time.Time `json:"run_started_at"`
}

func (w apiRun) run(repo string) Run {
	r := Run{
		ID:        w.ID,
		Repo:      repo,
		Workflow:  workflowName(w.Name),
		Title:     w.DisplayTitle,
		Branch:    w.HeadBranch,
		Event:     w.Event,
		SHA:       w.HeadSHA,
		Number:    w.RunNumber,
		Status:    checkRunStatus(w.Status, w.Conclusion),
		Waiting:   w.Status == "waiting",
		Cancelled: w.Conclusion == "cancelled",
		URL:       w.HTMLURL,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
		StartedAt: w.RunStartedAt,
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = r.CreatedAt
	}
	return r
}

// runsPage fetches one page of a repository's runs. With the ETag of the
// last answer, an unchanged page comes back as same, at no cost to the
// rate limit.
func (c *Client) runsPage(ctx context.Context, repo, u, etag string) (runs []Run, newETag string, same bool, err error) {
	resp, err := c.do(ctx, http.MethodGet, u, etag, nil)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	var r struct {
		WorkflowRuns []apiRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, "", false, fmt.Errorf("github: reading runs of %s: %w", repo, err)
	}
	for _, w := range r.WorkflowRuns {
		runs = append(runs, w.run(repo))
	}
	return runs, resp.Header.Get("ETag"), false, nil
}

func (c *Client) run(ctx context.Context, repo string, id int64) (Run, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/actions/runs/%d", c.BaseURL, repo, id), "", nil)
	if err != nil {
		return Run{}, err
	}
	defer resp.Body.Close()
	var w apiRun
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return Run{}, fmt.Errorf("github: reading run %d of %s: %w", id, repo, err)
	}
	return w.run(repo), nil
}

// Runs returns the workflow runs the actor triggered in a repository since
// the given day, newest first.
//
// A run is some 13 kB of JSON, so each poll asks for the newest page only,
// which an unchanged repository answers with a 304. The older runs are
// read once, and after that only those still running are asked for again,
// one by one.
func (c *Client) Runs(ctx context.Context, repo, actor string, since time.Time) ([]Run, error) {
	// The day, not the instant, so that the URL and its ETag last.
	day := since.UTC().Truncate(24 * time.Hour)
	pageURL := func(page int) string {
		q := url.Values{
			"actor":                 {actor},
			"per_page":              {strconv.Itoa(c.perPage)},
			"page":                  {strconv.Itoa(page)},
			"created":               {">=" + day.Format("2006-01-02")},
			"exclude_pull_requests": {"true"},
		}
		return fmt.Sprintf("%s/repos/%s/actions/runs?%s", c.BaseURL, repo, q.Encode())
	}

	// One poll at a time asks for a repository, so only the map is shared.
	c.mu.Lock()
	st := c.runs[repo]
	if st == nil {
		st = &repoRuns{runs: map[int64]Run{}}
		c.runs[repo] = st
	}
	c.mu.Unlock()

	u := pageURL(1)
	etag := ""
	if st.url == u {
		etag = st.etag
	}
	page, newETag, same, err := c.runsPage(ctx, repo, u, etag)
	if err != nil {
		return nil, err
	}
	// read holds the runs this poll's pages listed.
	read := map[int64]bool{}
	if !same {
		st.url, st.etag = u, newETag
		st.firstPage = map[int64]bool{}
		for _, r := range page {
			st.firstPage[r.ID] = true
		}
		// Read on while the pages are full: all of them the first time,
		// and later only through runs not seen before, as after a burst
		// of more than a page between two polls.
		for n := 2; ; n++ {
			allNew := true
			for _, r := range page {
				if _, known := st.runs[r.ID]; known {
					allNew = false
				}
				st.runs[r.ID] = r
				read[r.ID] = true
			}
			if len(page) < c.perPage || n > maxRunPages || (st.backfilled && !allNew) {
				break
			}
			if page, _, _, err = c.runsPage(ctx, repo, pageURL(n), ""); err != nil {
				return nil, err
			}
		}
		st.backfilled = true
	}

	// The first page says nothing of the runs past it that still run.
	followed := 0
	for id, r := range st.runs {
		if r.Status != StatusPending || st.firstPage[id] || read[id] || followed == maxFollowedRuns {
			continue
		}
		followed++
		got, err := c.run(ctx, repo, id)
		switch {
		case errors.Is(err, ErrNoAccess):
			delete(st.runs, id) // deleted on GitHub
		case err != nil:
			return nil, err
		default:
			st.runs[id] = got
		}
	}

	runs := make([]Run, 0, len(st.runs))
	for id, r := range st.runs {
		if r.CreatedAt.Before(day) {
			delete(st.runs, id)
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		}
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}
