// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package opml // import "miniflux.app/v2/internal/reader/opml"

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/handler"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/validator"
)

// Handler handles the logic for OPML import/export.
type Handler struct {
	store *storage.Storage
}

// Export exports user feeds to OPML.
func (h *Handler) Export(userID int64) (string, error) {
	feeds, err := h.store.Feeds(userID)
	if err != nil {
		return "", err
	}

	subscriptions := make([]subcription, 0, len(feeds))
	for _, feed := range feeds {
		subscriptions = append(subscriptions, subcription{
			Title:        feed.Title,
			FeedURL:      feed.FeedURL,
			SiteURL:      feed.SiteURL,
			Description:  feed.Description,
			CategoryName: feed.Category.Title,

			ScraperRules:                feed.ScraperRules,
			RewriteRules:                feed.RewriteRules,
			UrlRewriteRules:             feed.UrlRewriteRules,
			BlocklistRules:              feed.BlocklistRules,
			KeeplistRules:               feed.KeeplistRules,
			BlockFilterEntryRules:       feed.BlockFilterEntryRules,
			KeepFilterEntryRules:        feed.KeepFilterEntryRules,
			UserAgent:                   feed.UserAgent,
			Crawler:                     feed.Crawler,
			IgnoreHTTPCache:             feed.IgnoreHTTPCache,
			FetchViaProxy:               feed.FetchViaProxy,
			Disabled:                    feed.Disabled,
			NoMediaPlayer:               feed.NoMediaPlayer,
			HideGlobally:                feed.HideGlobally,
			AllowSelfSignedCertificates: feed.AllowSelfSignedCertificates,
			DisableHTTP2:                feed.DisableHTTP2,
			IgnoreEntryUpdates:          feed.IgnoreEntryUpdates,
		})
	}

	return serialize(subscriptions), nil
}

// Import parses an OPML document and bootstraps each subscription as a real feed.
//
// Instead of inserting bare feed records, every subscription is created through
// reader/handler.CreateFeed so the import performs the same work as adding a feed
// manually: the feed is fetched, its effective (post-redirect) URL is resolved,
// the ETag / Last-Modified validators are stored, the first batch of entries is
// processed and the favicon is initialized.
//
// The original OPML semantics are preserved: feeds that already exist are
// skipped, missing categories are created, and a single failing subscription
// does not abort the batch. Per-feed failures are collected and returned as one
// aggregated error, so the import is recoverable — re-running it skips the feeds
// that were imported successfully and retries the rest.
func (h *Handler) Import(userID int64, data io.Reader) error {
	subscriptions, err := parse(data)
	if err != nil {
		return err
	}

	// Resolve the user's language once so per-feed errors can be translated for
	// the aggregated summary. Translation is best-effort: a failure here must not
	// prevent the import itself.
	language := "en_US"
	if user, userErr := h.store.UserByID(userID); userErr == nil && user != nil && user.Language != "" {
		language = user.Language
	}

	var importErrors []string
	for _, subscription := range subscriptions {
		if h.store.FeedURLExists(userID, subscription.FeedURL) {
			slog.Debug("Skipping already subscribed feed during OPML import",
				slog.Int64("user_id", userID),
				slog.String("feed_url", subscription.FeedURL),
			)
			continue
		}

		category, categoryErr := h.resolveCategory(userID, subscription.CategoryName)
		if categoryErr != nil {
			slog.Warn("Unable to resolve category during OPML import",
				slog.Int64("user_id", userID),
				slog.String("feed_url", subscription.FeedURL),
				slog.Any("error", categoryErr),
			)
			importErrors = append(importErrors, fmt.Sprintf("%s: %v", subscription.FeedURL, categoryErr))
			continue
		}

		feedCreationRequest := buildFeedCreationRequest(category.ID, subscription)

		// Validate up front (URL shape, regular expressions, …) to fail fast and
		// avoid a pointless network fetch for malformed subscriptions.
		if validationErr := validator.ValidateFeedCreation(h.store, userID, feedCreationRequest); validationErr != nil {
			importErrors = append(importErrors, fmt.Sprintf("%s: %s", subscription.FeedURL, validationErr.Translate(language)))
			continue
		}

		createdFeed, localizedError := handler.CreateFeed(h.store, userID, feedCreationRequest)
		if localizedError != nil {
			// A feed whose effective (post-redirect) URL already exists is a
			// duplicate; skip it silently, like the pre-fetch check above.
			if errors.Is(localizedError.Error(), handler.ErrDuplicatedFeed) {
				slog.Debug("Skipping duplicated feed during OPML import",
					slog.Int64("user_id", userID),
					slog.String("feed_url", subscription.FeedURL),
				)
				continue
			}

			slog.Warn("Unable to import feed from OPML",
				slog.Int64("user_id", userID),
				slog.String("feed_url", subscription.FeedURL),
				slog.Any("error", localizedError.Error()),
			)
			importErrors = append(importErrors, fmt.Sprintf("%s: %s", subscription.FeedURL, localizedError.Translate(language)))
			continue
		}

		// CreateFeed derives the title and some metadata from the fetched feed and
		// does not carry every OPML-provided field. Restore the values the
		// importer is expected to preserve (the user-facing title and the
		// no-media-player flag).
		applyImportedFeedOverrides(h.store, createdFeed, subscription)
	}

	if len(importErrors) > 0 {
		return fmt.Errorf("opml: unable to import %d feed(s): %s", len(importErrors), strings.Join(importErrors, "; "))
	}

	return nil
}

func (h *Handler) resolveCategory(userID int64, categoryName string) (*model.Category, error) {
	if categoryName == "" {
		category, err := h.store.FirstCategory(userID)
		if err != nil {
			return nil, fmt.Errorf("opml: unable to find first category: %w", err)
		}
		return category, nil
	}

	category, err := h.store.CategoryByTitle(userID, categoryName)
	if err != nil {
		return nil, fmt.Errorf("opml: unable to search category by title: %w", err)
	}

	if category == nil {
		category, err = h.store.CreateCategory(userID, &model.CategoryCreationRequest{Title: categoryName})
		if err != nil {
			return nil, fmt.Errorf(`opml: unable to create this category: %q`, categoryName)
		}
	}

	return category, nil
}

// buildFeedCreationRequest maps an OPML subscription onto a feed creation request
// so it can be validated and bootstrapped through reader/handler.CreateFeed.
func buildFeedCreationRequest(categoryID int64, s subcription) *model.FeedCreationRequest {
	return &model.FeedCreationRequest{
		FeedURL:                     s.FeedURL,
		CategoryID:                  categoryID,
		UserAgent:                   s.UserAgent,
		Crawler:                     s.Crawler,
		IgnoreEntryUpdates:          s.IgnoreEntryUpdates,
		Disabled:                    s.Disabled,
		NoMediaPlayer:               s.NoMediaPlayer,
		IgnoreHTTPCache:             s.IgnoreHTTPCache,
		AllowSelfSignedCertificates: s.AllowSelfSignedCertificates,
		FetchViaProxy:               s.FetchViaProxy,
		HideGlobally:                s.HideGlobally,
		DisableHTTP2:                s.DisableHTTP2,
		ScraperRules:                s.ScraperRules,
		RewriteRules:                s.RewriteRules,
		BlocklistRules:              s.BlocklistRules,
		KeeplistRules:               s.KeeplistRules,
		BlockFilterEntryRules:       s.BlockFilterEntryRules,
		KeepFilterEntryRules:        s.KeepFilterEntryRules,
		UrlRewriteRules:             s.UrlRewriteRules,
	}
}

// applyImportedFeedOverrides restores OPML-provided values that CreateFeed does
// not set from the fetched feed: the user-facing title (when the OPML specifies
// one) and the no-media-player flag. The feed has already been persisted, so an
// update is only issued when one of those values actually changed.
//
// Failing to persist these overrides is not fatal: the feed itself was imported
// successfully, so the error is logged and the import continues.
func applyImportedFeedOverrides(store *storage.Storage, feed *model.Feed, s subcription) {
	changed := false

	if s.Title != "" && s.Title != feed.Title {
		feed.Title = s.Title
		changed = true
	}

	if s.NoMediaPlayer != feed.NoMediaPlayer {
		feed.NoMediaPlayer = s.NoMediaPlayer
		changed = true
	}

	if !changed {
		return
	}

	if err := store.UpdateFeed(feed); err != nil {
		slog.Warn("Unable to apply OPML feed settings after import",
			slog.Int64("user_id", feed.UserID),
			slog.Int64("feed_id", feed.ID),
			slog.String("feed_url", feed.FeedURL),
			slog.Any("error", err),
		)
	}
}

// NewHandler creates a new handler for OPML files.
func NewHandler(store *storage.Storage) *Handler {
	return &Handler{store: store}
}
