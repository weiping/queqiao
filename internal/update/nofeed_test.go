package update

import (
	"context"
	"errors"
	"testing"
)

func TestNoFeedWithoutEnv(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	if got := Feed(); got != "" {
		t.Fatalf("Feed() = %q, want empty", got)
	}
	if _, err := Latest(context.Background()); !errors.Is(err, ErrNoFeed) {
		t.Fatalf("Latest() err = %v, want ErrNoFeed", err)
	}
}
