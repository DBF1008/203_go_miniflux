// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cli // import "miniflux.app/v2/internal/cli"

import (
	"testing"

	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/urllib"
)

// These tests cover the batch composition that the two CLI batch-refresh entry points
// rely on:
//
//   - feedScheduler        (internal/cli/scheduler.go)     — the background scheduler
//   - refreshFeeds         (internal/cli/refresh_feeds.go) — the manual/CLI refresh
//
// Both build their batch via store.NewBatchBuilder().WithLimitPerHost(...).FetchJobs().
// FetchJobs streams the overdue feeds (ordered by next_check_at ascending) through the
// per-host fair selection exposed as storage.SelectJobsWithHostLimit. Exercising that
// selection directly keeps the tests independent of a live database while still
// asserting the behaviour both paths depend on: a single busy host must not starve
// feeds hosted elsewhere when POLLING_LIMIT_PER_HOST is enabled.

func dueJobsForHosts(hostsInOrder ...string) model.JobList {
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

func repeatHost(host string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = host
	}
	return out
}

func hostCounts(jobs model.JobList) map[string]int {
	counts := make(map[string]int)
	for _, job := range jobs {
		counts[urllib.Domain(job.FeedURL)]++
	}
	return counts
}

// TestSchedulerBatchAvoidsHostStarvation reproduces the scheduler scenario where a
// single host has accumulated far more overdue feeds than the batch size, and those
// feeds are all older than the other hosts' feeds. The busy host must be capped at the
// per-host limit and the remaining slots filled from the other hosts instead of being
// wasted (or handed back to the busy host), so no host is starved.
func TestSchedulerBatchAvoidsHostStarvation(t *testing.T) {
	const (
		batchSize    = 10
		limitPerHost = 2
	)

	otherHosts := []string{
		"news.example.com",
		"blog.example.net",
		"feeds.example.io",
		"planet.example.dev",
	}

	// busy.example.org has a large backlog of overdue feeds, all older than the rest.
	hostsInOrder := repeatHost("busy.example.org", 50)
	for _, host := range otherHosts {
		// Two feeds each, enough (with the busy host's quota) to fill the batch.
		hostsInOrder = append(hostsInOrder, host, host)
	}

	jobs := storage.SelectJobsWithHostLimit(dueJobsForHosts(hostsInOrder...), batchSize, limitPerHost)

	counts := hostCounts(jobs)
	if counts["busy.example.org"] != limitPerHost {
		t.Fatalf("busy host should be capped at %d per batch, got %d", limitPerHost, counts["busy.example.org"])
	}
	if len(jobs) != batchSize {
		t.Fatalf("expected the batch to be filled to %d, got %d (%v)", batchSize, len(jobs), counts)
	}
	for _, host := range otherHosts {
		if counts[host] == 0 {
			t.Fatalf("host %q was starved out of the scheduler batch: %v", host, counts)
		}
	}
}

// TestManualRefreshBatchAvoidsHostStarvation covers the manual/CLI refresh path. With a
// strict per-host limit, a host with a big backlog must not crowd the others out of the
// manual refresh batch.
func TestManualRefreshBatchAvoidsHostStarvation(t *testing.T) {
	const (
		batchSize    = 6
		limitPerHost = 1
	)

	otherHosts := []string{
		"a.example.com",
		"b.example.com",
		"c.example.com",
		"d.example.com",
		"e.example.com",
	}

	hostsInOrder := append(repeatHost("busy.example.org", 20), otherHosts...)

	jobs := storage.SelectJobsWithHostLimit(dueJobsForHosts(hostsInOrder...), batchSize, limitPerHost)

	counts := hostCounts(jobs)
	if counts["busy.example.org"] != limitPerHost {
		t.Fatalf("busy host should be capped at %d, got %d", limitPerHost, counts["busy.example.org"])
	}
	if len(jobs) != batchSize {
		t.Fatalf("expected manual refresh batch filled to %d, got %d (%v)", batchSize, len(jobs), counts)
	}
	for _, host := range otherHosts {
		if counts[host] != 1 {
			t.Fatalf("host %q should appear once in the manual refresh batch, got %d (starvation): %v", host, counts[host], counts)
		}
	}
}

// TestSchedulerBatchWithoutHostLimitKeepsOldestFirst confirms that when
// POLLING_LIMIT_PER_HOST is disabled (0), the batch keeps the original behaviour: the
// oldest overdue feeds first, regardless of host.
func TestSchedulerBatchWithoutHostLimitKeepsOldestFirst(t *testing.T) {
	hostsInOrder := append(repeatHost("busy.example.org", 5), "a.example.com", "b.example.com")

	jobs := storage.SelectJobsWithHostLimit(dueJobsForHosts(hostsInOrder...), 3, 0)

	if len(jobs) != 3 {
		t.Fatalf("expected batch of 3, got %d", len(jobs))
	}
	if c := hostCounts(jobs)["busy.example.org"]; c != 3 {
		t.Fatalf("without a per-host limit the 3 oldest feeds (all on the busy host) should be selected, got busy=%d", c)
	}
}
