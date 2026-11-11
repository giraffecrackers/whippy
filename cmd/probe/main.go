// Probe polls GitHub once, as the app does, and prints what it found: a
// check of gh's token and what it can read, without the windows.
//
//	go run ./cmd/probe
package main

import (
	"context"
	"fmt"
	"time"

	"whippy/internal/tracker"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	p := tracker.NewPoller(func(u tracker.Update) {
		failing, running := u.Snapshot.Counts(nil)
		fmt.Printf("auth=%v detail=%q loaded=%v err=%v login=%s prs=%d runs=%d failing=%d running=%d\n",
			u.Auth.State, u.Auth.Detail, u.Loaded, u.Err, u.Snapshot.Login, len(u.Snapshot.PRs), len(u.Snapshot.Runs), failing, running)
		for _, pr := range u.Snapshot.PRs {
			fmt.Printf("  PR  %s rollup=%q review=%s mergeable=%s checks=%d headRuns=%d\n",
				pr.Key(), pr.Rollup, pr.Review, pr.Mergeable, len(pr.Checks), len(u.Snapshot.RunsFor(pr)))
		}
		for _, r := range u.Snapshot.Runs {
			fmt.Printf("  run %s %s %s %q %s\n", r.Repo, r.Workflow, r.Branch, r.Status, r.CreatedAt.Format(time.RFC3339))
		}
		cancel()
	})
	p.Run(ctx)
}
