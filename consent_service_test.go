package main

import "testing"

func TestDecideConsentAndModeration(t *testing.T) {
	tests := []struct {
		name    string
		event   AssetEvent
		granted bool
		action  string
		reason  string
	}{
		{"revoked asset enters queue", AssetEvent{AssetID: "asset-1", Live: true}, false, "queue", "consent_required"},
		{"live consented asset publishes", AssetEvent{AssetID: "asset-2", Live: true}, true, "publish", "consent_granted"},
		{"moderation wins over publish", AssetEvent{AssetID: "asset-3", Live: true, Moderate: true}, true, "queue", "moderation_requested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.event, tt.granted)
			if got.Action != tt.action || got.Reason != tt.reason {
				t.Fatalf("got %#v, want %s/%s", got, tt.action, tt.reason)
			}
		})
	}
}
