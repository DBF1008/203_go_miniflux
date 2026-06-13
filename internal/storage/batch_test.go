// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

import (
	"strings"
	"testing"
)

// The manual "refresh all feeds" entry points (web UI and API) must cover every
// refreshable feed in scope, not only the ones whose next_check_at has expired. They
// build their batch through WithManualRefreshScope, so the generated query must not gate
// on next_check_at or parsing_error_count.
func TestManualRefreshAllFeedsQuery(t *testing.T) {
	query, args := (&Storage{}).NewBatchBuilder().
		WithManualRefreshScope(1).
		WithLimitPerHost(5).
		buildQuery()

	const want = `SELECT id, user_id, feed_url FROM feeds WHERE disabled IS false AND user_id = $1 ORDER BY next_check_at ASC`
	if query != want {
		t.Errorf("unexpected query.\n got: %q\nwant: %q", query, want)
	}

	if len(args) != 1 || args[0] != int64(1) {
		t.Errorf("unexpected args: %#v", args)
	}

	if strings.Contains(query, "next_check_at < now()") {
		t.Error("manual refresh-all query must not filter by next_check_at")
	}
	if strings.Contains(query, "parsing_error_count") {
		t.Error("manual refresh-all query must not filter by parsing_error_count")
	}
}

// Same guarantee as TestManualRefreshAllFeedsQuery, scoped to a single category.
func TestManualRefreshCategoryFeedsQuery(t *testing.T) {
	query, args := (&Storage{}).NewBatchBuilder().
		WithManualRefreshScope(1).
		WithCategoryID(2).
		WithLimitPerHost(5).
		buildQuery()

	const want = `SELECT id, user_id, feed_url FROM feeds WHERE disabled IS false AND user_id = $1 AND category_id = $2 ORDER BY next_check_at ASC`
	if query != want {
		t.Errorf("unexpected query.\n got: %q\nwant: %q", query, want)
	}

	if len(args) != 2 || args[0] != int64(1) || args[1] != int64(2) {
		t.Errorf("unexpected args: %#v", args)
	}

	if strings.Contains(query, "next_check_at < now()") {
		t.Error("manual category refresh query must not filter by next_check_at")
	}
	if strings.Contains(query, "parsing_error_count") {
		t.Error("manual category refresh query must not filter by parsing_error_count")
	}
}

// The scheduled (cron) refresh path must keep gating on next_check_at and the parsing
// error limit. This guards that the manual-refresh fix only changed the manual entry
// points, not the shared builder used by the scheduler.
func TestScheduledBatchQueryStillFiltersDueAndFailingFeeds(t *testing.T) {
	query, _ := (&Storage{}).NewBatchBuilder().
		WithBatchSize(100).
		WithErrorLimit(3).
		WithoutDisabledFeeds().
		WithNextCheckExpired().
		WithLimitPerHost(5).
		buildQuery()

	for _, fragment := range []string{
		"next_check_at < now()",
		"parsing_error_count < $",
		"disabled IS false",
		"LIMIT 100",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("scheduled batch query is missing %q.\ngot: %q", fragment, query)
		}
	}
}
