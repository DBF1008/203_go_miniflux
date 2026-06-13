// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"miniflux.app/v2/internal/model"
)

func TestSendEntryLogsLinkwardenCollectionID(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{ID: 52, URL: "https://example.org/test.html", Title: "Test"}
	coll := int64(12345)
	userIntegrations := &model.Integration{
		UserID:                 1,
		LinkwardenEnabled:      true,
		LinkwardenCollectionID: &coll,
		LinkwardenURL:          "",
		LinkwardenAPIKey:       "",
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"collection_id":12345`) {
		t.Fatalf("expected collection_id in logs; got: %s", out)
	}
}

func TestSendEntryLogsLinkwardenWithoutCollectionID(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{ID: 52, URL: "https://example.org/test.html", Title: "Test"}
	userIntegrations := &model.Integration{
		UserID:            1,
		LinkwardenEnabled: true,
		LinkwardenURL:     "",
		LinkwardenAPIKey:  "",
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if strings.Contains(out, "collection_id") {
		t.Fatalf("did not expect collection_id in logs; got: %s", out)
	}
}

func TestSendEntryFeedLevelWebhookWithoutGlobal(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{
		ID:  1,
		URL: "https://example.org/article.html",
		Feed: &model.Feed{
			ID:         1,
			UserID:     1,
			FeedURL:    "https://example.org/feed.xml",
			SiteURL:    "https://example.org",
			Title:      "Test Feed",
			WebhookURL: "http://127.0.0.1:1/webhook",
			Category:   &model.Category{ID: 1, Title: "Test"},
		},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: false,
		WebhookURL:     "",
		WebhookSecret:  "test-secret",
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"msg":"Sending entry to Webhook"`) {
		t.Fatalf("expected webhook dispatch log; got: %s", out)
	}
	if !strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/webhook"`) {
		t.Fatalf("expected feed-level webhook URL in log; got: %s", out)
	}
}

func TestSendEntryGlobalWebhookFallback(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{
		ID:  1,
		URL: "https://example.org/article.html",
		Feed: &model.Feed{
			ID:         1,
			UserID:     1,
			FeedURL:    "https://example.org/feed.xml",
			SiteURL:    "https://example.org",
			Title:      "Test Feed",
			WebhookURL: "",
			Category:   &model.Category{ID: 1, Title: "Test"},
		},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: true,
		WebhookURL:     "http://127.0.0.1:1/global-webhook",
		WebhookSecret:  "test-secret",
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"msg":"Sending entry to Webhook"`) {
		t.Fatalf("expected webhook dispatch log; got: %s", out)
	}
	if !strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/global-webhook"`) {
		t.Fatalf("expected global webhook URL in log; got: %s", out)
	}
}

func TestSendEntryFeedWebhookOverridesGlobal(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{
		ID:  1,
		URL: "https://example.org/article.html",
		Feed: &model.Feed{
			ID:         1,
			UserID:     1,
			FeedURL:    "https://example.org/feed.xml",
			SiteURL:    "https://example.org",
			Title:      "Test Feed",
			WebhookURL: "http://127.0.0.1:1/feed-webhook",
			Category:   &model.Category{ID: 1, Title: "Test"},
		},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: true,
		WebhookURL:     "http://127.0.0.1:1/global-webhook",
		WebhookSecret:  "test-secret",
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/feed-webhook"`) {
		t.Fatalf("expected feed-level URL to override global; got: %s", out)
	}
	if strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/global-webhook"`) {
		t.Fatalf("global URL should not appear when feed overrides; got: %s", out)
	}
}

func TestSendEntryNoWebhookWhenNothingConfigured(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	entry := &model.Entry{
		ID:  1,
		URL: "https://example.org/article.html",
		Feed: &model.Feed{
			ID:         1,
			UserID:     1,
			FeedURL:    "https://example.org/feed.xml",
			SiteURL:    "https://example.org",
			Title:      "Test Feed",
			WebhookURL: "",
			Category:   &model.Category{ID: 1, Title: "Test"},
		},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: false,
	}

	SendEntry(entry, userIntegrations)

	out := buf.String()
	if strings.Contains(out, "Webhook") {
		t.Fatalf("expected no webhook log when nothing configured; got: %s", out)
	}
}

func TestPushEntriesFeedLevelWebhookWithoutGlobal(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	feed := &model.Feed{
		ID:         1,
		UserID:     1,
		FeedURL:    "https://example.org/feed.xml",
		SiteURL:    "https://example.org",
		Title:      "Test Feed",
		WebhookURL: "http://127.0.0.1:1/feed-webhook",
		Category:   &model.Category{ID: 1, Title: "Test"},
	}
	entries := model.Entries{
		{ID: 1, URL: "https://example.org/1.html", FeedID: 1},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: false,
		WebhookSecret:  "test-secret",
	}

	PushEntries(feed, entries, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"msg":"Sending new entries to Webhook"`) {
		t.Fatalf("expected webhook dispatch log; got: %s", out)
	}
	if !strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/feed-webhook"`) {
		t.Fatalf("expected feed-level webhook URL in log; got: %s", out)
	}
}

func TestPushEntriesFeedWebhookOverridesGlobal(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	feed := &model.Feed{
		ID:         1,
		UserID:     1,
		FeedURL:    "https://example.org/feed.xml",
		SiteURL:    "https://example.org",
		Title:      "Test Feed",
		WebhookURL: "http://127.0.0.1:1/feed-webhook",
		Category:   &model.Category{ID: 1, Title: "Test"},
	}
	entries := model.Entries{
		{ID: 1, URL: "https://example.org/1.html", FeedID: 1},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: true,
		WebhookURL:     "http://127.0.0.1:1/global-webhook",
		WebhookSecret:  "test-secret",
	}

	PushEntries(feed, entries, userIntegrations)

	out := buf.String()
	if !strings.Contains(out, `"webhook_url":"http://127.0.0.1:1/feed-webhook"`) {
		t.Fatalf("expected feed-level URL to override global; got: %s", out)
	}
}

func TestPushEntriesNoWebhookWhenNothingConfigured(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	feed := &model.Feed{
		ID:         1,
		UserID:     1,
		FeedURL:    "https://example.org/feed.xml",
		SiteURL:    "https://example.org",
		Title:      "Test Feed",
		WebhookURL: "",
		Category:   &model.Category{ID: 1, Title: "Test"},
	}
	entries := model.Entries{
		{ID: 1, URL: "https://example.org/1.html", FeedID: 1},
	}
	userIntegrations := &model.Integration{
		UserID:         1,
		WebhookEnabled: false,
	}

	PushEntries(feed, entries, userIntegrations)

	out := buf.String()
	if strings.Contains(out, "Webhook") {
		t.Fatalf("expected no webhook log when nothing configured; got: %s", out)
	}
}
