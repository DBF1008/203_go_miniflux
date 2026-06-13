// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package opml // import "miniflux.app/v2/internal/reader/opml"

import (
	"reflect"
	"testing"

	"miniflux.app/v2/internal/model"
)

// TestBuildFeedCreationRequest ensures every OPML subscription setting that has a
// counterpart in model.FeedCreationRequest is carried over (and the category ID
// is wired in) before a feed is bootstrapped through reader/handler.CreateFeed.
func TestBuildFeedCreationRequest(t *testing.T) {
	source := subcription{
		// Fields without a FeedCreationRequest counterpart: CreateFeed derives
		// title/site URL/description from the fetched feed, and the category is
		// passed separately as an ID.
		Title:        "Example Feed",
		SiteURL:      "https://example.org/",
		CategoryName: "Tech",
		Description:  "An example feed",

		FeedURL:                     "https://example.org/feed.xml",
		ScraperRules:                "scraper-rules",
		RewriteRules:                "rewrite-rules",
		UrlRewriteRules:             "urlrewrite-rules",
		BlocklistRules:              "blocklist-rules",
		KeeplistRules:               "keeplist-rules",
		BlockFilterEntryRules:       "block-filter-rules",
		KeepFilterEntryRules:        "keep-filter-rules",
		UserAgent:                   "custom-user-agent",
		Crawler:                     true,
		IgnoreHTTPCache:             true,
		FetchViaProxy:               true,
		Disabled:                    true,
		NoMediaPlayer:               true,
		HideGlobally:                true,
		AllowSelfSignedCertificates: true,
		DisableHTTP2:                true,
		IgnoreEntryUpdates:          true,
	}

	expected := &model.FeedCreationRequest{
		FeedURL:                     "https://example.org/feed.xml",
		CategoryID:                  42,
		UserAgent:                   "custom-user-agent",
		Crawler:                     true,
		IgnoreEntryUpdates:          true,
		Disabled:                    true,
		NoMediaPlayer:               true,
		IgnoreHTTPCache:             true,
		AllowSelfSignedCertificates: true,
		FetchViaProxy:               true,
		HideGlobally:                true,
		DisableHTTP2:                true,
		ScraperRules:                "scraper-rules",
		RewriteRules:                "rewrite-rules",
		BlocklistRules:              "blocklist-rules",
		KeeplistRules:               "keeplist-rules",
		BlockFilterEntryRules:       "block-filter-rules",
		KeepFilterEntryRules:        "keep-filter-rules",
		UrlRewriteRules:             "urlrewrite-rules",
	}

	result := buildFeedCreationRequest(42, source)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("buildFeedCreationRequest() mismatch:\ngot:  %#v\nwant: %#v", result, expected)
	}
}
