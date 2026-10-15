// Package syncer implements the incremental local cache of a chat's
// conversations.
package syncer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/store"
)

// Overlap is subtracted from the stored high-water marks so that rows written
// around the previous sync boundary are re-listed.
const Overlap = 5 * time.Minute

// Options tunes a sync run.
type Options struct {
	Full     bool
	Progress func(format string, a ...any) // optional
	Now      func() time.Time              // optional, defaults to time.Now
}

// Summary is the result of a sync run.
type Summary struct {
	ChatID  string `json:"chat_id"`
	Full    bool   `json:"full"`
	Listed  int    `json:"listed"`
	Fetched int    `json:"fetched"`
	Skipped int    `json:"skipped"`
	Errors  int    `json:"errors"`
	Cached  int    `json:"cached_total,omitempty"`
	// CacheRebuilt is true when the marks were ignored because the cache was
	// empty or smaller than at the last sync.
	CacheRebuilt bool   `json:"cache_rebuilt,omitempty"`
	Since        string `json:"since,omitempty"`
	AdvancedTo   string `json:"updated_at_high_water,omitempty"`
}

// Run lists changed conversations, fetches the ones that are new or changed
// and saves them. High-water marks advance only when every save succeeded.
func Run(ctx context.Context, c *client.Client, st *store.Store, o Options) (*Summary, error) {
	progress := o.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	state, err := st.State()
	if err != nil {
		return nil, err
	}
	sum := &Summary{ChatID: st.ChatID, Full: o.Full}

	// The marks are only trustworthy while the cache still holds what the last
	// sync stored; otherwise list everything again (marks are kept).
	ignoreMarks := false
	hasMarks := state.UpdatedAtHighWater != nil || state.LastMessageAtHighWater != nil
	if !o.Full && hasMarks {
		if n := st.CountConversations(); n == 0 || n < state.CachedTotal {
			progress("local cache has %d conversations but the last sync stored %d: listing everything", n, state.CachedTotal)
			sum.CacheRebuilt = true
			ignoreMarks = true
		}
	}
	var updSince, msgSince time.Time
	if !o.Full && !ignoreMarks {
		if state.UpdatedAtHighWater != nil {
			updSince = state.UpdatedAtHighWater.Add(-Overlap)
			sum.Since = updSince.UTC().Format(time.RFC3339)
		}
		if state.LastMessageAtHighWater != nil {
			msgSince = state.LastMessageAtHighWater.Add(-Overlap)
		}
	}

	union := map[int64]client.Conversation{}
	collect := func(opts client.ListConversationsOptions) error {
		return c.EachConversation(ctx, st.ChatID, opts, func(cv client.Conversation) error {
			if _, ok := union[cv.ID]; !ok {
				union[cv.ID] = cv
			}
			return nil
		})
	}
	progress("listing conversations changed since %s ...", orAll(sum.Since))
	if err := collect(client.ListConversationsOptions{UpdatedAtSince: updSince}); err != nil {
		return sum, fmt.Errorf("listing by updated_at: %w", err)
	}
	if !updSince.IsZero() || !msgSince.IsZero() { // a full listing already covers everything
		if err := collect(client.ListConversationsOptions{LastMessageAtSince: msgSince}); err != nil {
			return sum, fmt.Errorf("listing by last_message_at: %w", err)
		}
	}

	ids := make([]int64, 0, len(union))
	for id := range union {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sum.Listed = len(ids)

	maxUpd, maxMsg := ptrVal(state.UpdatedAtHighWater), ptrVal(state.LastMessageAtHighWater)
	if o.Full {
		maxUpd, maxMsg = time.Time{}, time.Time{}
	}
	var firstErr error
	for i, id := range ids {
		item := union[id]
		if item.UpdatedAt.After(maxUpd) {
			maxUpd = item.UpdatedAt
		}
		if item.LastMessageAt != nil && item.LastMessageAt.After(maxMsg) {
			maxMsg = *item.LastMessageAt
		}
		if !o.Full {
			if cached, err := st.LoadConversation(id); err == nil && unchanged(cached, &item) {
				sum.Skipped++
				continue
			}
		}
		full, err := c.GetConversation(ctx, st.ChatID, id)
		if err == nil {
			err = st.SaveConversation(full)
		}
		if err != nil {
			sum.Errors++
			if firstErr == nil {
				firstErr = fmt.Errorf("conversation %d: %w", id, err)
			}
			progress("error on conversation %d: %v", id, err)
			continue
		}
		sum.Fetched++
		if (i+1)%25 == 0 {
			progress("synced %d/%d (fetched %d, skipped %d, errors %d)", i+1, len(ids), sum.Fetched, sum.Skipped, sum.Errors)
		}
	}
	progress("done: listed %d, fetched %d, skipped %d, errors %d", sum.Listed, sum.Fetched, sum.Skipped, sum.Errors)
	if firstErr != nil {
		return sum, fmt.Errorf("sync incomplete, high-water marks not advanced (%d errors): %w", sum.Errors, firstErr)
	}

	t := now().UTC()
	state.LastSync = &t
	if o.Full {
		state.LastFullSync = &t
	}
	if !maxUpd.IsZero() {
		state.UpdatedAtHighWater = &maxUpd
		sum.AdvancedTo = maxUpd.UTC().Format(time.RFC3339)
	}
	if !maxMsg.IsZero() {
		state.LastMessageAtHighWater = &maxMsg
	}
	if err := st.SaveState(state); err != nil {
		return sum, err
	}
	if all, err := st.LoadAll(nil, nil); err == nil {
		sum.Cached = len(all)
		state.CachedTotal = len(all)
		if err := st.SaveState(state); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

func unchanged(cached, item *client.Conversation) bool {
	if !cached.UpdatedAt.Equal(item.UpdatedAt) {
		return false
	}
	switch {
	case cached.LastMessageAt == nil && item.LastMessageAt == nil:
		return true
	case cached.LastMessageAt == nil || item.LastMessageAt == nil:
		return false
	}
	return cached.LastMessageAt.Equal(*item.LastMessageAt)
}

func ptrVal(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func orAll(s string) string {
	if s == "" {
		return "the beginning"
	}
	return s
}
