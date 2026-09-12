package providerproto

import (
	"encoding/json"
	"testing"
)

// These tests pin the wire format itself: a provider author in any language
// depends on these exact field names and shapes, so an accidental json tag
// change here should fail loudly.
func TestWireFormat(t *testing.T) {
	t.Run("ResolveResult", func(t *testing.T) {
		b, err := json.Marshal(ResolveResult{Value: "hunter2"})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"value":"hunter2"}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("ValidateResult", func(t *testing.T) {
		b, err := json.Marshal(ValidateResult{OK: true})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"ok":true}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("InitResult", func(t *testing.T) {
		b, err := json.Marshal(InitResult{Env: map[string]string{"AWS_REGION": "us-east-1"}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"env":{"AWS_REGION":"us-east-1"}}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("ErrorEnvelope", func(t *testing.T) {
		b, err := json.Marshal(ErrorEnvelope{Error: ErrorPayload{Kind: KindNotFound, Message: "no such secret"}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"error":{"kind":"not_found","message":"no such secret"}}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
}
