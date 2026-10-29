package security

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBcryptHasher(t *testing.T) {
	h := BcryptHasher{Cost: bcrypt.MinCost}
	hash, err := h.Hash("secret123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "secret123" {
		t.Fatal("hash must not equal the password")
	}
	if err := h.Compare(hash, "secret123"); err != nil {
		t.Fatalf("correct password must match: %v", err)
	}
	if err := h.Compare(hash, "wrong-password"); err == nil {
		t.Fatal("wrong password must not match")
	}
	if err := h.Compare("", "secret123"); err == nil {
		t.Fatal("empty hash must not match")
	}
	other, _ := h.Hash("secret123")
	if other == hash {
		t.Fatal("hashes of the same password must differ (salt)")
	}
}
