// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"fmt"
	"testing"

	"miniflux.app/v2/internal/model"
)

func makeJob(id int64, feedURL string) model.Job {
	return model.Job{FeedID: id, UserID: 1, FeedURL: feedURL}
}

func makeJobsForHost(hostname string, startID int64, count int) model.JobList {
	jobs := make(model.JobList, count)
	for i := range count {
		jobs[i] = makeJob(startID+int64(i), fmt.Sprintf("https://%s/feed/%d", hostname, i))
	}
	return jobs
}

// --- Tests for filterJobsForBatch (core fairness logic) ---

func TestFilterJobsForBatchNoLimits(t *testing.T) {
	candidates := model.JobList{
		makeJob(1, "https://a.com/feed"),
		makeJob(2, "https://b.com/feed"),
		makeJob(3, "https://c.com/feed"),
	}

	jobs, skipped := filterJobsForBatch(candidates, 0, 0)

	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
}

func TestFilterJobsForBatchBatchSizeOnly(t *testing.T) {
	candidates := makeJobsForHost("a.com", 1, 10)

	jobs, skipped := filterJobsForBatch(candidates, 5, 0)

	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(jobs))
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
}

func TestFilterJobsForBatchHostLimitOnly(t *testing.T) {
	candidates := model.JobList{
		makeJob(1, "https://a.com/feed1"),
		makeJob(2, "https://a.com/feed2"),
		makeJob(3, "https://a.com/feed3"),
		makeJob(4, "https://b.com/feed1"),
		makeJob(5, "https://b.com/feed2"),
	}

	jobs, skipped := filterJobsForBatch(candidates, 0, 2)

	if len(jobs) != 4 {
		t.Fatalf("expected 4 jobs (2 per host), got %d", len(jobs))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", skipped)
	}
}

// TestFilterJobsForBatchDominantHostNoStarvation is the key regression test.
// With the old code (SQL LIMIT applied before per-host filtering), host A would
// fill the SQL result set and host B feeds would be starved.
// The fix ensures that feeds from host B are included even when host A
// dominates the candidate list.
func TestFilterJobsForBatchDominantHostNoStarvation(t *testing.T) {
	var candidates model.JobList

	// Host A: 95 feeds with lower IDs (older next_check_at — appear first)
	candidates = append(candidates, makeJobsForHost("a.com", 1, 95)...)
	// Host B: 50 feeds with higher IDs (newer next_check_at — appear later)
	candidates = append(candidates, makeJobsForHost("b.com", 100, 50)...)

	batchSize := 20
	limitPerHost := 5

	jobs, skipped := filterJobsForBatch(candidates, batchSize, limitPerHost)

	// Count jobs per host
	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := ""
		if job.FeedID < 100 {
			host = "a.com"
		} else {
			host = "b.com"
		}
		hostCounts[host]++
	}

	if hostCounts["a.com"] != 5 {
		t.Errorf("expected 5 jobs from a.com, got %d", hostCounts["a.com"])
	}
	if hostCounts["b.com"] != 5 {
		t.Errorf("expected 5 jobs from b.com (no starvation), got %d", hostCounts["b.com"])
	}
	if len(jobs) != 10 {
		t.Errorf("expected 10 total jobs (5 per host), got %d", len(jobs))
	}

	// 90 from A skipped (95-5), 45 from B skipped (50-5)
	expectedSkipped := 90 + 45
	if skipped != expectedSkipped {
		t.Errorf("expected %d skipped, got %d", expectedSkipped, skipped)
	}
}

// TestFilterJobsForBatchFullBatchDespiteDominantHost verifies that the batch
// is filled up to batchSize even when one host dominates the candidate list.
// This is the primary starvation fix: the old code would return only
// limitPerHost jobs because the SQL LIMIT capped the candidate rows.
func TestFilterJobsForBatchFullBatchDespiteDominantHost(t *testing.T) {
	var candidates model.JobList

	// Host A: 100 feeds (dominant — all appear before host B)
	candidates = append(candidates, makeJobsForHost("a.com", 1, 100)...)
	// Hosts B through J: 10 feeds each (9 hosts × 3 per-host = 27 from non-dominant)
	for i, host := range []string{"b.com", "c.com", "d.com", "e.com", "f.com", "g.com", "h.com", "i.com", "j.com"} {
		candidates = append(candidates, makeJobsForHost(host, 200+int64(i)*100, 10)...)
	}

	batchSize := 20
	limitPerHost := 3

	jobs, _ := filterJobsForBatch(candidates, batchSize, limitPerHost)

	// 1 from dominant host (3) + 9 non-dominant hosts (3 each = 27) = 30 possible, capped at batchSize=20
	if len(jobs) != batchSize {
		t.Fatalf("expected %d jobs (full batch), got %d", batchSize, len(jobs))
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}

	if hostCounts["a.com"] != 3 {
		t.Errorf("expected 3 jobs from a.com, got %d", hostCounts["a.com"])
	}

	// The remaining 17 slots should be filled by the other 9 hosts (3 each = 27 available, but only 17 needed)
	otherTotal := 0
	for host, count := range hostCounts {
		if host != "a.com" {
			otherTotal += count
			if count > 3 {
				t.Errorf("host %s exceeded per-host limit: got %d, max 3", host, count)
			}
		}
	}
	if otherTotal != 17 {
		t.Errorf("expected 17 jobs from non-dominant hosts, got %d", otherTotal)
	}
}

func TestFilterJobsForBatchManyHostsFairness(t *testing.T) {
	var candidates model.JobList

	// 10 hosts, each with 20 feeds
	for i := range 10 {
		hostname := fmt.Sprintf("host%d.com", i)
		candidates = append(candidates, makeJobsForHost(hostname, int64(i*100), 20)...)
	}

	batchSize := 50
	limitPerHost := 3

	jobs, _ := filterJobsForBatch(candidates, batchSize, limitPerHost)

	// 10 hosts * 3 = 30, batchSize = 50, so only 30 jobs possible
	if len(jobs) != 30 {
		t.Fatalf("expected 30 jobs (10 hosts * 3 per host), got %d", len(jobs))
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}

	for i := range 10 {
		hostname := fmt.Sprintf("host%d.com", i)
		if hostCounts[hostname] != 3 {
			t.Errorf("expected 3 jobs from %s, got %d", hostname, hostCounts[hostname])
		}
	}
}

func TestFilterJobsForBatchEmptyCandidates(t *testing.T) {
	jobs, skipped := filterJobsForBatch(nil, 10, 5)
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
}

func TestFilterJobsForBatchBatchSizeLargerThanCandidates(t *testing.T) {
	candidates := makeJobsForHost("a.com", 1, 5)

	jobs, _ := filterJobsForBatch(candidates, 100, 0)

	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(jobs))
	}
}

func TestFilterJobsForBatchAllSameHostAtLimit(t *testing.T) {
	candidates := makeJobsForHost("a.com", 1, 20)

	jobs, skipped := filterJobsForBatch(candidates, 10, 5)

	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs (per-host limit), got %d", len(jobs))
	}
	if skipped != 15 {
		t.Fatalf("expected 15 skipped, got %d", skipped)
	}
}

func TestFilterJobsForBatchHostLimitOne(t *testing.T) {
	var candidates model.JobList
	candidates = append(candidates, makeJobsForHost("a.com", 1, 5)...)
	candidates = append(candidates, makeJobsForHost("b.com", 10, 5)...)
	candidates = append(candidates, makeJobsForHost("c.com", 20, 5)...)

	jobs, skipped := filterJobsForBatch(candidates, 10, 1)

	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs (1 per host), got %d", len(jobs))
	}
	if skipped != 12 {
		t.Fatalf("expected 12 skipped, got %d", skipped)
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}
	for _, host := range []string{"a.com", "b.com", "c.com"} {
		if hostCounts[host] != 1 {
			t.Errorf("expected 1 job from %s, got %d", host, hostCounts[host])
		}
	}
}

func TestFilterJobsForBatchInterleavedHosts(t *testing.T) {
	// Simulate feeds ordered by next_check_at where hosts are interleaved
	candidates := model.JobList{
		makeJob(1, "https://a.com/feed1"),
		makeJob(2, "https://b.com/feed1"),
		makeJob(3, "https://a.com/feed2"),
		makeJob(4, "https://c.com/feed1"),
		makeJob(5, "https://b.com/feed2"),
		makeJob(6, "https://a.com/feed3"),
		makeJob(7, "https://c.com/feed2"),
		makeJob(8, "https://b.com/feed3"),
		makeJob(9, "https://a.com/feed4"),
		makeJob(10, "https://c.com/feed3"),
	}

	jobs, skipped := filterJobsForBatch(candidates, 10, 2)

	// Each host has 3-4 feeds, limit is 2: a=2, b=2, c=2 = 6 jobs
	if len(jobs) != 6 {
		t.Fatalf("expected 6 jobs, got %d", len(jobs))
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}
	for _, host := range []string{"a.com", "b.com", "c.com"} {
		if hostCounts[host] != 2 {
			t.Errorf("expected 2 jobs from %s, got %d", host, hostCounts[host])
		}
	}

	// a: 2 skipped (feeds 3,4), b: 1 skipped (feed 3), c: 1 skipped (feed 3)
	if skipped != 4 {
		t.Errorf("expected 4 skipped, got %d", skipped)
	}
}

func TestFilterJobsForBatchPreservesOrderWithinHost(t *testing.T) {
	candidates := model.JobList{
		makeJob(1, "https://a.com/feed1"),
		makeJob(2, "https://a.com/feed2"),
		makeJob(3, "https://b.com/feed1"),
		makeJob(4, "https://a.com/feed3"),
		makeJob(5, "https://b.com/feed2"),
	}

	jobs, _ := filterJobsForBatch(candidates, 10, 2)

	if len(jobs) != 4 {
		t.Fatalf("expected 4 jobs, got %d", len(jobs))
	}

	// a.com feeds should be in order: feed1, feed2
	aFeeds := []int64{}
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		if host == "a.com" {
			aFeeds = append(aFeeds, job.FeedID)
		}
	}
	if len(aFeeds) != 2 || aFeeds[0] != 1 || aFeeds[1] != 2 {
		t.Errorf("expected a.com feeds [1, 2], got %v", aFeeds)
	}
}

// --- Scheduler simulation tests ---
// These simulate the batch selection that the background scheduler
// (feedScheduler in cli/scheduler.go) performs every tick.

func TestSchedulerSimulation_DominantHostDoesNotStarve(t *testing.T) {
	// Simulates what the scheduler sees: a large set of expired feeds where
	// one host dominates the oldest entries. The scheduler uses:
	//   WithBatchSize(20).WithLimitPerHost(5).WithNextCheckExpired()
	var candidates model.JobList

	// Host "popular-blog.example": 80 feeds all due for check
	candidates = append(candidates, makeJobsForHost("popular-blog.example", 1, 80)...)
	// 8 other hosts with 5 feeds each (also due)
	for i, host := range []string{
		"news.example", "tech.example", "science.example", "sports.example",
		"music.example", "art.example", "food.example", "travel.example",
	} {
		candidates = append(candidates, makeJobsForHost(host, int64(1000+i*100), 5)...)
	}

	batchSize := 20
	limitPerHost := 5

	jobs, _ := filterJobsForBatch(candidates, batchSize, limitPerHost)

	// Batch should be full: 5 from popular-blog + 15 from others
	if len(jobs) != batchSize {
		t.Fatalf("scheduler batch should be full (%d), got %d", batchSize, len(jobs))
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}

	if hostCounts["popular-blog.example"] != 5 {
		t.Errorf("expected 5 from popular-blog, got %d", hostCounts["popular-blog.example"])
	}

	// At least some other hosts should be represented
	otherHosts := 0
	for host, count := range hostCounts {
		if host != "popular-blog.example" {
			otherHosts++
			if count > limitPerHost {
				t.Errorf("host %s exceeded limit: %d", host, count)
			}
		}
	}
	if otherHosts == 0 {
		t.Fatal("expected feeds from other hosts in the batch (starvation detected)")
	}
	// Should have 3 other hosts (each with 5 = 15 total)
	if otherHosts != 3 {
		t.Errorf("expected 3 other hosts in batch, got %d", otherHosts)
	}
}

func TestSchedulerSimulation_ZeroLimitPerHost(t *testing.T) {
	// When POLLING_LIMIT_PER_HOST=0, per-host limiting is disabled.
	// The scheduler should fill the batch purely by next_check_at order.
	var candidates model.JobList
	candidates = append(candidates, makeJobsForHost("a.com", 1, 50)...)
	candidates = append(candidates, makeJobsForHost("b.com", 100, 50)...)

	jobs, skipped := filterJobsForBatch(candidates, 30, 0)

	if len(jobs) != 30 {
		t.Fatalf("expected 30 jobs, got %d", len(jobs))
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped when limitPerHost=0, got %d", skipped)
	}

	// All 30 should be from a.com (they have lower IDs = older next_check_at)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		if host != "a.com" {
			t.Errorf("expected only a.com jobs when limitPerHost=0, got %s", host)
		}
	}
}

// --- Manual batch refresh simulation tests ---
// These simulate the batch selection that manual refresh paths perform:
//   - API refreshAllFeedsHandler / refreshCategoryHandler
//   - UI refreshAllFeeds / refreshCategory

func TestManualRefresh_DominantHostFairness(t *testing.T) {
	// API refreshAllFeeds uses: WithErrorLimit().WithNextCheckExpired().WithUserID().WithLimitPerHost()
	// (no BatchSize — fetches all user's expired feeds)
	var candidates model.JobList
	candidates = append(candidates, makeJobsForHost("dominant.example", 1, 50)...)
	candidates = append(candidates, makeJobsForHost("other1.example", 100, 10)...)
	candidates = append(candidates, makeJobsForHost("other2.example", 200, 10)...)

	// No batchSize (0), limitPerHost=5
	jobs, _ := filterJobsForBatch(candidates, 0, 5)

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}

	if hostCounts["dominant.example"] != 5 {
		t.Errorf("expected 5 from dominant host, got %d", hostCounts["dominant.example"])
	}
	if hostCounts["other1.example"] != 5 {
		t.Errorf("expected 5 from other1, got %d", hostCounts["other1.example"])
	}
	if hostCounts["other2.example"] != 5 {
		t.Errorf("expected 5 from other2, got %d", hostCounts["other2.example"])
	}
	if len(jobs) != 15 {
		t.Fatalf("expected 15 total jobs, got %d", len(jobs))
	}
}

func TestManualRefresh_ForceRefreshNoFilters(t *testing.T) {
	// UI force-refresh uses: WithoutDisabledFeeds().WithUserID().WithLimitPerHost()
	// (no BatchSize, no ErrorLimit, no NextCheckExpired — refreshes everything)
	var candidates model.JobList
	candidates = append(candidates, makeJobsForHost("a.example", 1, 20)...)
	candidates = append(candidates, makeJobsForHost("b.example", 100, 20)...)

	jobs, _ := filterJobsForBatch(candidates, 0, 10)

	if len(jobs) != 20 {
		t.Fatalf("expected 20 jobs (10 per host), got %d", len(jobs))
	}

	hostCounts := make(map[string]int)
	for _, job := range jobs {
		host := extractHostFromURL(job.FeedURL)
		hostCounts[host]++
	}
	if hostCounts["a.example"] != 10 {
		t.Errorf("expected 10 from a.example, got %d", hostCounts["a.example"])
	}
	if hostCounts["b.example"] != 10 {
		t.Errorf("expected 10 from b.example, got %d", hostCounts["b.example"])
	}
}

// TestMultiRoundSchedulerSimulation simulates multiple scheduler ticks.
// After each round, the selected feeds would have their next_check_at pushed
// into the future (making them ineligible for the next round). We simulate
// this by removing selected feeds from the candidate list.
func TestMultiRoundSchedulerSimulation(t *testing.T) {
	// 2 hosts: A has 30 feeds, B has 30 feeds
	var allFeeds model.JobList
	allFeeds = append(allFeeds, makeJobsForHost("a.example", 1, 30)...)
	allFeeds = append(allFeeds, makeJobsForHost("b.example", 100, 30)...)

	batchSize := 10
	limitPerHost := 3

	// Track how many times each feed is selected across rounds
	selectedCounts := make(map[int64]int)
	totalSelectedPerHost := make(map[string]int)

	for round := range 5 {
		// Build candidates: all feeds not yet selected in this simulation
		// (simulating that selected feeds get their next_check_at pushed forward)
		var candidates model.JobList
		for _, feed := range allFeeds {
			if selectedCounts[feed.FeedID] == 0 {
				candidates = append(candidates, feed)
			}
		}

		jobs, _ := filterJobsForBatch(candidates, batchSize, limitPerHost)

		for _, job := range jobs {
			selectedCounts[job.FeedID]++
			host := extractHostFromURL(job.FeedURL)
			totalSelectedPerHost[host]++
		}

		if len(jobs) == 0 {
			t.Logf("round %d: no more jobs available", round)
			break
		}
	}

	// After 5 rounds with batchSize=10 and limitPerHost=3:
	// Each round should select 6 from A (if limit allows) and fill the rest from B
	// But with only 3 per host, each round = 6 total (3+3), so 5 rounds = 30 total
	// (or fewer if we run out of feeds)
	aTotal := totalSelectedPerHost["a.example"]
	bTotal := totalSelectedPerHost["b.example"]

	// Both hosts should get equal treatment
	if aTotal != bTotal {
		t.Errorf("expected equal selection across hosts: a=%d, b=%d", aTotal, bTotal)
	}

	// Each host should have been selected in multiple rounds
	if aTotal < 9 { // At least 3 rounds * 3 per host
		t.Errorf("expected at least 9 selections from each host, got a=%d, b=%d", aTotal, bTotal)
	}
}

// extractHostFromURL extracts the host from a URL string for test assertions.
func extractHostFromURL(rawURL string) string {
	// Simple extraction: "https://host/path" -> "host"
	// This mirrors urllib.Domain for test purposes.
	start := 0
	if i := indexAfterScheme(rawURL); i >= 0 {
		start = i
	}
	end := len(rawURL)
	for i := start; i < len(rawURL); i++ {
		if rawURL[i] == '/' || rawURL[i] == '?' || rawURL[i] == '#' {
			end = i
			break
		}
	}
	return rawURL[start:end]
}

func indexAfterScheme(rawURL string) int {
	for i := 0; i < len(rawURL)-2; i++ {
		if rawURL[i] == ':' && rawURL[i+1] == '/' && rawURL[i+2] == '/' {
			return i + 3
		}
	}
	return -1
}
