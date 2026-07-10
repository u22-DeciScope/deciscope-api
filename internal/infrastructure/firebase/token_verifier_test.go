package firebase

import "testing"

func TestNewTokenVerifierNilClientReturnsNilInterface(t *testing.T) {
	if verifier := NewTokenVerifier(nil); verifier != nil {
		t.Fatalf("NewTokenVerifier(nil) = %#v, want nil", verifier)
	}
}
