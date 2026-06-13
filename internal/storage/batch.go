// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/urllib"
)

type batchBuilder struct {
	db           *sql.DB
	args         []any
	conditions   []string
	batchSize    int
	limitPerHost int
}

func (s *Storage) NewBatchBuilder() *batchBuilder {
	return &batchBuilder{
		db: s.db,
	}
}

func (b *batchBuilder) WithBatchSize(batchSize int) *batchBuilder {
	b.batchSize = batchSize
	return b
}

func (b *batchBuilder) WithUserID(userID int64) *batchBuilder {
	b.conditions = append(b.conditions, "user_id = $"+strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, userID)
	return b
}

func (b *batchBuilder) WithCategoryID(categoryID int64) *batchBuilder {
	b.conditions = append(b.conditions, "category_id = $"+strconv.Itoa(len(b.args)+1))
	b.args = append(b.args, categoryID)
	return b
}

func (b *batchBuilder) WithErrorLimit(limit int) *batchBuilder {
	if limit > 0 {
		b.conditions = append(b.conditions, "parsing_error_count < $"+strconv.Itoa(len(b.args)+1))
		b.args = append(b.args, limit)
	}
	return b
}

func (b *batchBuilder) WithNextCheckExpired() *batchBuilder {
	b.conditions = append(b.conditions, "next_check_at < now()")
	return b
}

func (b *batchBuilder) WithoutDisabledFeeds() *batchBuilder {
	b.conditions = append(b.conditions, "disabled IS false")
	return b
}

func (b *batchBuilder) WithLimitPerHost(limit int) *batchBuilder {
	if limit > 0 {
		b.limitPerHost = limit
	}
	return b
}

// FetchJobs retrieves a batch of jobs based on the conditions set in the builder.
// When limitPerHost is set, it limits the number of jobs per feed hostname to prevent overwhelming a single host.
func (b *batchBuilder) FetchJobs() (model.JobList, error) {
	query := `SELECT id, user_id, feed_url FROM feeds`

	if len(b.conditions) > 0 {
		query += " WHERE " + strings.Join(b.conditions, " AND ")
	}

	query += " ORDER BY next_check_at ASC"

	// When a per-host limit is enforced, the batch cannot be bounded with a SQL
	// LIMIT: a single host with a large backlog of overdue feeds would fill the
	// whole window and, once its per-host quota is reached, the remaining slots
	// would simply be skipped — leaving feeds hosted elsewhere starved batch after
	// batch. Instead we stream the candidates in next_check_at order and let the
	// selector enforce both the per-host quota and the batch size, scanning far
	// enough to fill the batch fairly from other hosts (it stops early once the
	// batch is full). Without a per-host limit, the batch size maps directly to a
	// SQL LIMIT and the common case stays as cheap as before.
	if b.batchSize > 0 && b.limitPerHost <= 0 {
		query += " LIMIT " + strconv.Itoa(b.batchSize)
	}

	rows, err := b.db.Query(query, b.args...)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch batch of jobs: %v`, err)
	}
	defer rows.Close()

	selector := newJobSelector(b.batchSize, b.limitPerHost)
	nbRows := 0

	for rows.Next() {
		var job model.Job
		if err := rows.Scan(&job.FeedID, &job.UserID, &job.FeedURL); err != nil {
			return nil, fmt.Errorf(`store: unable to fetch job record: %v`, err)
		}

		nbRows++

		if selector.offer(job) {
			break
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(`store: error iterating on job records: %v`, err)
	}

	slog.Info("Created a batch of feeds",
		slog.Int("batch_size", b.batchSize),
		slog.Int("rows_count", nbRows),
		slog.Int("skipped_feeds_count", selector.skipped),
		slog.Int("jobs_count", len(selector.jobs)),
	)

	return selector.jobs, nil
}

// jobSelector builds a batch of jobs while optionally enforcing a per-host limit.
//
// Jobs must be offered in next_check_at ascending order (oldest due first). When a
// per-host limit is set and a host has already reached it, the offered job is skipped
// so that jobs from other hosts — which appear later in the ordering — can still fill
// the batch. This prevents a single host with a large backlog of overdue feeds from
// monopolizing every batch and starving feeds hosted elsewhere.
type jobSelector struct {
	batchSize    int
	limitPerHost int
	hostCounts   map[string]int
	jobs         model.JobList
	skipped      int
}

func newJobSelector(batchSize, limitPerHost int) *jobSelector {
	capacity := batchSize
	if capacity < 0 {
		capacity = 0
	}
	return &jobSelector{
		batchSize:    batchSize,
		limitPerHost: limitPerHost,
		hostCounts:   make(map[string]int),
		jobs:         make(model.JobList, 0, capacity),
	}
}

// offer considers a single candidate job and returns true once the batch is full,
// signalling that no further jobs need to be offered. Jobs whose host has reached the
// per-host limit are skipped rather than ending the batch, so later jobs from other
// hosts can still be selected.
func (s *jobSelector) offer(job model.Job) (full bool) {
	if s.limitPerHost > 0 {
		feedHostname := urllib.Domain(job.FeedURL)
		if s.hostCounts[feedHostname] >= s.limitPerHost {
			s.skipped++
			return s.isFull()
		}
		s.hostCounts[feedHostname]++
	}

	s.jobs = append(s.jobs, job)
	return s.isFull()
}

func (s *jobSelector) isFull() bool {
	return s.batchSize > 0 && len(s.jobs) >= s.batchSize
}

// SelectJobsWithHostLimit selects up to batchSize jobs from candidates while allowing at
// most limitPerHost jobs per feed hostname (limitPerHost <= 0 disables the per-host
// limit). Candidates must be ordered by next_check_at ascending (oldest due first).
//
// Jobs from a host that has reached the limit are skipped so the remaining batch slots
// can be filled fairly from other hosts, preventing a single busy host from starving
// feeds hosted elsewhere over successive batches. This is the shared selection that
// FetchJobs applies (while streaming) for every batch-refresh entry point: the
// background scheduler, the manual batch refresh, and the API/UI refresh handlers.
func SelectJobsWithHostLimit(candidates model.JobList, batchSize, limitPerHost int) model.JobList {
	selector := newJobSelector(batchSize, limitPerHost)
	for _, job := range candidates {
		if selector.offer(job) {
			break
		}
	}
	return selector.jobs
}
