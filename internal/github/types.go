package github

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Status is GitHub's many check and run states folded into the few the app
// shows.
type Status string

const (
	StatusNone    Status = ""
	StatusPending Status = "pending"
	StatusSuccess Status = "success"
	StatusFailure Status = "failure"
	// StatusSkipped covers skipped, neutral and cancelled: finished, and
	// nobody needs to look.
	StatusSkipped Status = "skipped"
)

const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewRequired         = "REVIEW_REQUIRED"

	MergeableConflicting = "CONFLICTING"

	PRMerged = "MERGED"
	PRClosed = "CLOSED"
)

type Check struct {
	Name   string
	Status Status
	URL    string
}

type PR struct {
	Repo      string // owner/name
	Number    int
	Title     string
	URL       string
	Branch    string
	HeadSHA   string
	Draft     bool
	Review    string // ReviewApproved, …, or "" where no review is required
	Mergeable string
	// Rollup is the combined state of the head commit's checks.
	Rollup Status
	// Checks is empty where the token may not read check runs, as a
	// fine-grained personal access token may not. Rollup is still set.
	Checks    []Check
	UpdatedAt time.Time
}

func (p PR) Key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

type Run struct {
	ID       int64
	Repo     string
	Workflow string
	Title    string
	Branch   string
	Event    string
	SHA      string
	Number   int
	Status   Status
	// Waiting is set while a pending run waits for someone, as for the
	// approval of a deployment, rather than for a machine.
	Waiting   bool
	Cancelled bool
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
	// StartedAt is when this attempt started: later than CreatedAt for a
	// run that was run again.
	StartedAt time.Time
}

// PullRequests is one poll's answer about the viewer's pull requests.
type PullRequests struct {
	Login string
	Open  []PR
	// Closed maps PR.Key to PRMerged or PRClosed for the viewer's pull
	// requests that were recently merged or closed.
	Closed map[string]string
	// RecentRepos are the repositories of the viewer's recently updated
	// pull requests, open or not.
	RecentRepos []string
}

func checkRunStatus(status, conclusion string) Status {
	if !strings.EqualFold(status, "completed") {
		return StatusPending
	}
	switch strings.ToLower(conclusion) {
	case "success":
		return StatusSuccess
	case "failure", "timed_out", "startup_failure", "action_required":
		return StatusFailure
	default:
		return StatusSkipped
	}
}

func commitStatus(state string) Status {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return StatusSuccess
	case "FAILURE", "ERROR":
		return StatusFailure
	case "PENDING", "EXPECTED":
		return StatusPending
	default:
		return StatusNone
	}
}

// workflowName shortens the path GitHub reports for a workflow with no name.
func workflowName(name string) string {
	if strings.HasPrefix(name, ".github/") {
		return path.Base(name)
	}
	return name
}
