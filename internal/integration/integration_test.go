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

func TestWebhookURLForFeed(t *testing.T) {
	const feedURL = "https://example.org/feed-hook"
	const globalURL = "https://example.org/global-hook"

	tests := []struct {
		name     string
		feed     *model.Feed
		enabled  bool
		global   string
		expected string
	}{
		{
			name:     "feed URL works when global webhook is disabled",
			feed:     &model.Feed{WebhookURL: feedURL},
			enabled:  false,
			global:   "",
			expected: feedURL,
		},
		{
			name:     "feed URL overrides global URL",
			feed:     &model.Feed{WebhookURL: feedURL},
			enabled:  true,
			global:   globalURL,
			expected: feedURL,
		},
		{
			name:     "global URL used when feed has none and webhook is enabled",
			feed:     &model.Feed{},
			enabled:  true,
			global:   globalURL,
			expected: globalURL,
		},
		{
			name:     "no webhook when feed has none and global is disabled",
			feed:     &model.Feed{},
			enabled:  false,
			global:   globalURL,
			expected: "",
		},
		{
			name:     "nil feed falls back to enabled global URL",
			feed:     nil,
			enabled:  true,
			global:   globalURL,
			expected: globalURL,
		},
		{
			name:     "nil feed with disabled global yields no webhook",
			feed:     nil,
			enabled:  false,
			global:   globalURL,
			expected: "",
		},
		{
			name:     "enabled global with empty URL yields no webhook",
			feed:     &model.Feed{},
			enabled:  true,
			global:   "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userIntegrations := &model.Integration{
				WebhookEnabled: tc.enabled,
				WebhookURL:     tc.global,
				WebhookSecret:  "shared-secret",
			}
			if got := webhookURLForFeed(tc.feed, userIntegrations); got != tc.expected {
				t.Errorf("webhookURLForFeed() = %q, want %q", got, tc.expected)
			}
		})
	}
}
