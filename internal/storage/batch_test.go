// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"reflect"
	"testing"

	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/urllib"
)

// jobsForHosts builds a JobList whose feed URLs resolve to the given hostnames, in the
// provided order. Candidates passed to the selector must already be ordered by
// next_check_at ascending, so the slice order represents "oldest due first".
func jobsForHosts(hostsInOrder ...string) model.JobList {
	jobs := make(model.JobList, 0, len(hostsInOrder))
	for i, host := range hostsInOrder {
		jobs = append(jobs, model.Job{
			FeedID:  int64(i + 1),
			UserID:  1,
			FeedURL: "https://" + host + "/feed.xml",
		})
	}
	return jobs
}

// repeatHost returns n copies of host, used to simulate a host with a backlog.
func repeatHost(host string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = host
	}
	return out
}

func hostsOf(jobs model.JobList) []string {
	hosts := make([]string, len(jobs))
	for i, job := range jobs {
		hosts[i] = urllib.Domain(job.FeedURL)
	}
	return hosts
}

func countByHost(jobs model.JobList) map[string]int {
	counts := make(map[string]int)
	for _, job := range jobs {
		counts[urllib.Domain(job.FeedURL)]++
	}
	return counts
}

func TestSelectJobsWithHostLimitDisabled(t *testing.T) {
	// With limitPerHost <= 0 the selection keeps the original behaviour: the first
	// batchSize candidates in next_check_at order, regardless of host.
	candidates := jobsForHosts(
		"busy.example.org", "busy.example.org", "busy.example.org",
		"a.example.org", "b.example.org",
	)

	got := SelectJobsWithHostLimit(candidates, 3, 0)

	want := []string{"busy.example.org", "busy.example.org", "busy.example.org"}
	if h := hostsOf(got); !reflect.DeepEqual(h, want) {
		t.Fatalf("expected %v, got %v", want, h)
	}
}

func TestSelectJobsWithHostLimitPreventsHostStarvation(t *testing.T) {
	// A busy host has a backlog of overdue feeds that are all older (earlier in the
	// next_check_at ordering) than every other host's feeds. The previous logic
	// applied the batch size as a SQL LIMIT before the per-host filtering, so the whole
	// window was consumed by the busy host: after its quota was reached the rest of the
	// batch was skipped and the other hosts never made it in, batch after batch.
	//
	// The fair selector must cap the busy host and fill the remaining slots from the
	// other hosts that appear later in the ordering.
	hostsInOrder := append(repeatHost("busy.example.org", 10),
		"a.example.org", "b.example.org", "c.example.org", "d.example.org")
	candidates := jobsForHosts(hostsInOrder...)

	got := SelectJobsWithHostLimit(candidates, 5, 2)

	wantHosts := []string{
		"busy.example.org", "busy.example.org",
		"a.example.org", "b.example.org", "c.example.org",
	}
	if h := hostsOf(got); !reflect.DeepEqual(h, wantHosts) {
		t.Fatalf("expected fair batch %v, got %v", wantHosts, h)
	}

	counts := countByHost(got)
	if counts["busy.example.org"] != 2 {
		t.Fatalf("expected busy host capped at 2, got %d", counts["busy.example.org"])
	}
	if len(counts) < 2 {
		t.Fatalf("expected feeds from multiple hosts (no starvation), got only %v", hostsOf(got))
	}
}

func TestSelectJobsWithHostLimitFillsBatchSize(t *testing.T) {
	// Every host is under the limit, so the batch is simply filled to batchSize in
	// next_check_at order.
	candidates := jobsForHosts(
		"a.example.org", "b.example.org", "c.example.org",
		"d.example.org", "e.example.org", "f.example.org",
	)

	got := SelectJobsWithHostLimit(candidates, 4, 2)

	want := []string{"a.example.org", "b.example.org", "c.example.org", "d.example.org"}
	if h := hostsOf(got); !reflect.DeepEqual(h, want) {
		t.Fatalf("expected %v, got %v", want, h)
	}
}

func TestSelectJobsWithHostLimitReturnsAllBelowBatchSize(t *testing.T) {
	// Fewer eligible candidates than the batch size: return them all without scanning
	// past the end.
	candidates := jobsForHosts("a.example.org", "b.example.org")

	got := SelectJobsWithHostLimit(candidates, 10, 2)

	if len(got) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(got))
	}
}

func TestJobSelectorSkippedCountAndNotFull(t *testing.T) {
	// A host exceeding its limit contributes only limitPerHost jobs; the overflow is
	// counted as skipped, and the selector is not considered full while the batch size
	// has not been reached.
	selector := newJobSelector(10, 2)
	for _, job := range jobsForHosts(repeatHost("busy.example.org", 5)...) {
		if selector.offer(job) {
			t.Fatalf("selector should not be full with batch size 10 and only 2 accepted jobs")
		}
	}

	if len(selector.jobs) != 2 {
		t.Fatalf("expected 2 accepted jobs, got %d", len(selector.jobs))
	}
	if selector.skipped != 3 {
		t.Fatalf("expected 3 skipped jobs, got %d", selector.skipped)
	}
}

func TestJobSelectorStopsWhenBatchFull(t *testing.T) {
	// Without a per-host limit the selector reports full exactly when batchSize jobs
	// have been collected, so the caller can stop scanning early.
	selector := newJobSelector(3, 0)
	candidates := jobsForHosts(
		"a.example.org", "b.example.org", "c.example.org",
		"d.example.org", "e.example.org",
	)

	fullAt := -1
	for i, job := range candidates {
		if selector.offer(job) {
			fullAt = i
			break
		}
	}

	if fullAt != 2 {
		t.Fatalf("expected selector to become full after offering 3 jobs (index 2), got index %d", fullAt)
	}
	if len(selector.jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(selector.jobs))
	}
}

func TestJobSelectorNoSkipWhenUnderLimit(t *testing.T) {
	// A host repeated within its limit is fully accepted with no skips.
	selector := newJobSelector(5, 3)
	for _, job := range jobsForHosts("busy.example.org", "busy.example.org", "a.example.org", "b.example.org") {
		selector.offer(job)
	}

	if selector.skipped != 0 {
		t.Fatalf("expected no skipped jobs, got %d", selector.skipped)
	}
	if len(selector.jobs) != 4 {
		t.Fatalf("expected 4 jobs, got %d", len(selector.jobs))
	}
}
