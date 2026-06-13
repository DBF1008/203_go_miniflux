// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

import (
	"strings"
	"testing"
)

// newTestBatchBuilder returns a batchBuilder without a database handle,
// suitable for testing the condition/query-building logic only.
func newTestBatchBuilder() *batchBuilder {
	return &batchBuilder{}
}

func TestBatchBuilderWithUserID(t *testing.T) {
	b := newTestBatchBuilder().WithUserID(42)

	if len(b.conditions) != 1 {
		t.Fatalf("Expected 1 condition, got %d", len(b.conditions))
	}
	if b.conditions[0] != "user_id = $1" {
		t.Fatalf("Expected condition %q, got %q", "user_id = $1", b.conditions[0])
	}
	if len(b.args) != 1 || b.args[0] != int64(42) {
		t.Fatalf("Expected args [42], got %v", b.args)
	}
}

func TestBatchBuilderWithCategoryID(t *testing.T) {
	b := newTestBatchBuilder().WithCategoryID(7)

	if len(b.conditions) != 1 {
		t.Fatalf("Expected 1 condition, got %d", len(b.conditions))
	}
	if b.conditions[0] != "category_id = $1" {
		t.Fatalf("Expected condition %q, got %q", "category_id = $1", b.conditions[0])
	}
	if len(b.args) != 1 || b.args[0] != int64(7) {
		t.Fatalf("Expected args [7], got %v", b.args)
	}
}

func TestBatchBuilderWithErrorLimit(t *testing.T) {
	b := newTestBatchBuilder().WithErrorLimit(3)

	if len(b.conditions) != 1 {
		t.Fatalf("Expected 1 condition, got %d", len(b.conditions))
	}
	if b.conditions[0] != "parsing_error_count < $1" {
		t.Fatalf("Expected condition %q, got %q", "parsing_error_count < $1", b.conditions[0])
	}
	if len(b.args) != 1 || b.args[0] != 3 {
		t.Fatalf("Expected args [3], got %v", b.args)
	}
}

func TestBatchBuilderWithErrorLimitZeroIsIgnored(t *testing.T) {
	b := newTestBatchBuilder().WithErrorLimit(0)

	if len(b.conditions) != 0 {
		t.Fatalf("Expected 0 conditions when limit is 0, got %d: %v", len(b.conditions), b.conditions)
	}
	if len(b.args) != 0 {
		t.Fatalf("Expected 0 args when limit is 0, got %d", len(b.args))
	}
}

func TestBatchBuilderWithErrorLimitNegativeIsIgnored(t *testing.T) {
	b := newTestBatchBuilder().WithErrorLimit(-1)

	if len(b.conditions) != 0 {
		t.Fatalf("Expected 0 conditions when limit is negative, got %d: %v", len(b.conditions), b.conditions)
	}
}

func TestBatchBuilderWithNextCheckExpired(t *testing.T) {
	b := newTestBatchBuilder().WithNextCheckExpired()

	if len(b.conditions) != 1 {
		t.Fatalf("Expected 1 condition, got %d", len(b.conditions))
	}
	if b.conditions[0] != "next_check_at < now()" {
		t.Fatalf("Expected condition %q, got %q", "next_check_at < now()", b.conditions[0])
	}
	// WithNextCheckExpired should not add any args.
	if len(b.args) != 0 {
		t.Fatalf("Expected 0 args, got %d", len(b.args))
	}
}

func TestBatchBuilderWithoutDisabledFeeds(t *testing.T) {
	b := newTestBatchBuilder().WithoutDisabledFeeds()

	if len(b.conditions) != 1 {
		t.Fatalf("Expected 1 condition, got %d", len(b.conditions))
	}
	if b.conditions[0] != "disabled IS false" {
		t.Fatalf("Expected condition %q, got %q", "disabled IS false", b.conditions[0])
	}
	if len(b.args) != 0 {
		t.Fatalf("Expected 0 args, got %d", len(b.args))
	}
}

func TestBatchBuilderWithBatchSize(t *testing.T) {
	b := newTestBatchBuilder().WithBatchSize(100)

	if b.batchSize != 100 {
		t.Fatalf("Expected batchSize 100, got %d", b.batchSize)
	}
}

func TestBatchBuilderWithLimitPerHost(t *testing.T) {
	b := newTestBatchBuilder().WithLimitPerHost(5)

	if b.limitPerHost != 5 {
		t.Fatalf("Expected limitPerHost 5, got %d", b.limitPerHost)
	}
}

func TestBatchBuilderWithLimitPerHostZeroIsIgnored(t *testing.T) {
	b := newTestBatchBuilder().WithLimitPerHost(0)

	if b.limitPerHost != 0 {
		t.Fatalf("Expected limitPerHost 0, got %d", b.limitPerHost)
	}
}

func TestBatchBuilderChainingMultipleConditions(t *testing.T) {
	b := newTestBatchBuilder().
		WithErrorLimit(5).
		WithoutDisabledFeeds().
		WithNextCheckExpired().
		WithUserID(10).
		WithCategoryID(20).
		WithLimitPerHost(3).
		WithBatchSize(50)

	expectedConditions := []string{
		"parsing_error_count < $1",
		"disabled IS false",
		"next_check_at < now()",
		"user_id = $2",
		"category_id = $3",
	}
	if len(b.conditions) != len(expectedConditions) {
		t.Fatalf("Expected %d conditions, got %d: %v", len(expectedConditions), len(b.conditions), b.conditions)
	}
	for i, cond := range expectedConditions {
		if b.conditions[i] != cond {
			t.Errorf("Condition %d: expected %q, got %q", i, cond, b.conditions[i])
		}
	}

	// args: 5 (error limit), 10 (user_id), 20 (category_id)
	expectedArgs := []any{5, int64(10), int64(20)}
	if len(b.args) != len(expectedArgs) {
		t.Fatalf("Expected %d args, got %d: %v", len(expectedArgs), len(b.args), b.args)
	}
	for i, arg := range expectedArgs {
		if b.args[i] != arg {
			t.Errorf("Arg %d: expected %v, got %v", i, arg, b.args[i])
		}
	}

	if b.limitPerHost != 3 {
		t.Errorf("Expected limitPerHost 3, got %d", b.limitPerHost)
	}
	if b.batchSize != 50 {
		t.Errorf("Expected batchSize 50, got %d", b.batchSize)
	}
}

// TestBatchBuilderManualRefreshAllFeedsConditions verifies that a manual
// "refresh all feeds" query from the UI or API does NOT include
// next_check_at or parsing_error_count conditions. This is the core
// regression test for the bug where manual refresh silently skipped
// feeds whose next_check_at had not yet expired.
func TestBatchBuilderManualRefreshAllFeedsConditions(t *testing.T) {
	b := newTestBatchBuilder().
		WithoutDisabledFeeds().
		WithUserID(1).
		WithLimitPerHost(2)

	// Must contain "disabled IS false" and "user_id = $1"
	if len(b.conditions) != 2 {
		t.Fatalf("Expected exactly 2 conditions for manual refresh, got %d: %v", len(b.conditions), b.conditions)
	}

	query := strings.Join(b.conditions, " AND ")
	if strings.Contains(query, "next_check_at") {
		t.Fatal("Manual refresh must NOT filter by next_check_at")
	}
	if strings.Contains(query, "parsing_error_count") {
		t.Fatal("Manual refresh must NOT filter by parsing_error_count")
	}
	if !strings.Contains(query, "disabled IS false") {
		t.Fatal("Manual refresh must filter disabled feeds")
	}
	if !strings.Contains(query, "user_id = $1") {
		t.Fatal("Manual refresh must filter by user_id")
	}

	if b.limitPerHost != 2 {
		t.Fatalf("Expected limitPerHost 2, got %d", b.limitPerHost)
	}
}

// TestBatchBuilderManualRefreshCategoryFeedsConditions verifies that a
// manual "refresh category feeds" query does NOT include next_check_at
// or parsing_error_count conditions but DOES include the category filter.
func TestBatchBuilderManualRefreshCategoryFeedsConditions(t *testing.T) {
	b := newTestBatchBuilder().
		WithoutDisabledFeeds().
		WithUserID(1).
		WithCategoryID(42).
		WithLimitPerHost(2)

	if len(b.conditions) != 3 {
		t.Fatalf("Expected exactly 3 conditions for manual category refresh, got %d: %v", len(b.conditions), b.conditions)
	}

	query := strings.Join(b.conditions, " AND ")
	if strings.Contains(query, "next_check_at") {
		t.Fatal("Manual category refresh must NOT filter by next_check_at")
	}
	if strings.Contains(query, "parsing_error_count") {
		t.Fatal("Manual category refresh must NOT filter by parsing_error_count")
	}
	if !strings.Contains(query, "disabled IS false") {
		t.Fatal("Manual category refresh must filter disabled feeds")
	}
	if !strings.Contains(query, "user_id = $1") {
		t.Fatal("Manual category refresh must filter by user_id")
	}
	if !strings.Contains(query, "category_id = $2") {
		t.Fatal("Manual category refresh must filter by category_id")
	}
}

// TestBatchBuilderSchedulerConditions verifies that the automated
// scheduler path correctly includes all filters (error limit, disabled,
// next_check_at). This is the expected behavior for background polling,
// as opposed to manual refresh.
func TestBatchBuilderSchedulerConditions(t *testing.T) {
	b := newTestBatchBuilder().
		WithErrorLimit(3).
		WithoutDisabledFeeds().
		WithNextCheckExpired().
		WithBatchSize(100)

	if len(b.conditions) != 3 {
		t.Fatalf("Expected 3 conditions for scheduler, got %d: %v", len(b.conditions), b.conditions)
	}

	query := strings.Join(b.conditions, " AND ")
	if !strings.Contains(query, "parsing_error_count < $1") {
		t.Fatal("Scheduler must filter by parsing_error_count")
	}
	if !strings.Contains(query, "disabled IS false") {
		t.Fatal("Scheduler must filter disabled feeds")
	}
	if !strings.Contains(query, "next_check_at < now()") {
		t.Fatal("Scheduler must filter by expired next_check_at")
	}

	if b.batchSize != 100 {
		t.Fatalf("Expected batchSize 100, got %d", b.batchSize)
	}
}

// TestBatchBuilderNoConditions verifies that a builder with no conditions
// produces an empty conditions slice (for FetchJobs, this means SELECT
// without a WHERE clause).
func TestBatchBuilderNoConditions(t *testing.T) {
	b := newTestBatchBuilder()

	if len(b.conditions) != 0 {
		t.Fatalf("Expected 0 conditions, got %d", len(b.conditions))
	}
	if len(b.args) != 0 {
		t.Fatalf("Expected 0 args, got %d", len(b.args))
	}
	if b.batchSize != 0 {
		t.Fatalf("Expected batchSize 0, got %d", b.batchSize)
	}
	if b.limitPerHost != 0 {
		t.Fatalf("Expected limitPerHost 0, got %d", b.limitPerHost)
	}
}

// TestBatchBuilderArgNumbering verifies that positional parameter
// placeholders ($1, $2, ...) are numbered correctly regardless of the
// order in which builder methods are called.
func TestBatchBuilderArgNumbering(t *testing.T) {
	b := newTestBatchBuilder().
		WithUserID(100).
		WithErrorLimit(5).
		WithCategoryID(200)

	expectedConditions := []string{
		"user_id = $1",
		"parsing_error_count < $2",
		"category_id = $3",
	}
	if len(b.conditions) != len(expectedConditions) {
		t.Fatalf("Expected %d conditions, got %d", len(expectedConditions), len(b.conditions))
	}
	for i, cond := range expectedConditions {
		if b.conditions[i] != cond {
			t.Errorf("Condition %d: expected %q, got %q", i, cond, b.conditions[i])
		}
	}

	expectedArgs := []any{int64(100), 5, int64(200)}
	if len(b.args) != len(expectedArgs) {
		t.Fatalf("Expected %d args, got %d", len(expectedArgs), len(b.args))
	}
	for i, arg := range expectedArgs {
		if b.args[i] != arg {
			t.Errorf("Arg %d: expected %v, got %v", i, arg, b.args[i])
		}
	}
}
