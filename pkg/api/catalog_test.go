package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

// The catalog carries entitlements the table does not print, and a caller that
// drops them advertises models it cannot use. This is the wire shape, kept in
// the test as a contract rather than fetched.
func TestModelInfoKeepsEntitlementFields(t *testing.T) {
	raw := `[
	  {"botId":"gpt-6.1-sol","premiumOnly":true,"lifetimeOnly":false,"tier":"advanced","creditWeight":5,"active":true,"group":"chat"},
	  {"botId":"gpt-6-luna","premiumOnly":false,"lifetimeOnly":false,"tier":"basic","creditWeight":1,"active":true,"group":"chat"},
	  {"botId":"gpt-image-2.5-sunburst","premiumOnly":false,"group":"image","active":true}
	]`

	var models []api.ModelInfo
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("want 3 models, got %d", len(models))
	}
	if !models[0].PremiumOnly || models[0].Tier != "advanced" || models[0].CreditWeight != 5 {
		t.Fatalf("premium entry lost its entitlement fields: %+v", models[0])
	}
	if models[1].PremiumOnly || models[1].CreditWeight != 1 {
		t.Fatalf("free entry decoded wrong: %+v", models[1])
	}
	if models[2].Group != "image" {
		t.Fatalf("image entry decoded wrong: %+v", models[2])
	}
}

type stubTransport func(*http.Request) (*http.Response, error)

func (f stubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// An unreachable catalog must not leave the image path without a model: the
// fallback is the id that answered on 2026-11-06, not the retired gpt-image-2.
func TestPickImageModel(t *testing.T) {
	offline := api.NewClient()
	offline.HTTPClient = &http.Client{Transport: stubTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}

	if got := offline.PickImageModel("gpt-image-2.5-flare"); got != "gpt-image-2.5-flare" {
		t.Fatalf("explicit model was overridden: %q", got)
	}
	if got := offline.PickImageModel(""); got != "gpt-image-2.5-sunburst" {
		t.Fatalf("fallback model = %q, want gpt-image-2.5-sunburst", got)
	}
}
