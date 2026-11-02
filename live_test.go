//go:build live

package fastmail_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ionut-maxim/fastmail"
)

// TestLiveList checks the client against the real API. It only reads.
// Run it with: FASTMAIL_TOKEN=... go test -tags=live -run TestLive -v .
func TestLiveList(t *testing.T) {
	token := os.Getenv("FASTMAIL_TOKEN")
	if token == "" {
		t.Skip("FASTMAIL_TOKEN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := fastmail.New(token)
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.MaskedEmail.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, m := range list {
		if m.ID == "" || m.Email == "" || !m.State.Valid() {
			t.Errorf("incomplete masked email: id=%q state=%q", m.ID, m.State)
		}
		counts[string(m.State)]++
	}
	// Log counts only, never addresses.
	t.Logf("%d masked emails by state: %v", len(list), counts)
}
