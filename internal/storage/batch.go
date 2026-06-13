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
// When limitPerHost is set, it limits the number of jobs per feed hostname to prevent
// overwhelming a single host. To ensure fairness across hosts (avoiding starvation when
// a single host dominates the oldest feeds), the per-host cap is enforced in Go rather
// than via SQL LIMIT so that skipped slots are backfilled by feeds from other hosts.
func (b *batchBuilder) FetchJobs() (model.JobList, error) {
	query := `SELECT id, user_id, feed_url FROM feeds`

	if len(b.conditions) > 0 {
		query += " WHERE " + strings.Join(b.conditions, " AND ")
	}

	query += " ORDER BY next_check_at ASC"

	// When limitPerHost is active, we intentionally omit the SQL LIMIT.
	// A SQL-level LIMIT would cap the candidate rows before per-host filtering,
	// causing feeds from non-dominant hosts to be starved when a single host
	// occupies most of the top rows. The Go-side filter below caps the returned
	// batch at batchSize after applying per-host limits, ensuring fairness.
	if b.batchSize > 0 && b.limitPerHost <= 0 {
		query += " LIMIT " + strconv.Itoa(b.batchSize)
	}

	rows, err := b.db.Query(query, b.args...)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to fetch batch of jobs: %v`, err)
	}
	defer rows.Close()

	var allJobs model.JobList
	nbRows := 0

	for rows.Next() {
		var job model.Job
		if err := rows.Scan(&job.FeedID, &job.UserID, &job.FeedURL); err != nil {
			return nil, fmt.Errorf(`store: unable to fetch job record: %v`, err)
		}
		allJobs = append(allJobs, job)
		nbRows++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(`store: error iterating on job records: %v`, err)
	}

	jobs, nbSkippedFeeds := filterJobsForBatch(allJobs, b.batchSize, b.limitPerHost)

	slog.Info("Created a batch of feeds",
		slog.Int("batch_size", b.batchSize),
		slog.Int("rows_count", nbRows),
		slog.Int("skipped_feeds_count", nbSkippedFeeds),
		slog.Int("jobs_count", len(jobs)),
	)

	return jobs, nil
}

// filterJobsForBatch applies per-host and batch-size limits to an ordered list
// of candidate jobs. It returns the filtered job list and the number of jobs
// that were skipped due to the per-host limit. Because the iteration covers all
// candidates (not just a SQL-LIMITed prefix), feeds from non-dominant hosts are
// not starved when a single host owns many of the oldest next_check_at values.
func filterJobsForBatch(candidates model.JobList, batchSize, limitPerHost int) (model.JobList, int) {
	jobs := make(model.JobList, 0, batchSize)
	hosts := make(map[string]int)
	nbSkipped := 0

	for _, job := range candidates {
		if batchSize > 0 && len(jobs) >= batchSize {
			break
		}

		if limitPerHost > 0 {
			feedHostname := urllib.Domain(job.FeedURL)
			if hosts[feedHostname] >= limitPerHost {
				slog.Debug("Feed host limit reached for this batch",
					slog.String("feed_url", job.FeedURL),
					slog.String("feed_hostname", feedHostname),
					slog.Int("limit_per_host", limitPerHost),
					slog.Int("current", hosts[feedHostname]),
				)
				nbSkipped++
				continue
			}
			hosts[feedHostname]++
		}

		jobs = append(jobs, job)
	}

	return jobs, nbSkipped
}
