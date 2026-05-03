package storage

import (
	"encoding/hex"
	"testing"
)

func TestSha256Hex(t *testing.T) {
	// Known test vector: SHA-256 of empty string
	got := sha256Hex([]byte{})
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Fatalf("sha256Hex(empty) = %q, want %q", got, want)
	}

	// SHA-256 of "hello"
	got = sha256Hex([]byte("hello"))
	want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("sha256Hex(hello) = %q, want %q", got, want)
	}
}

func TestHmacSHA256(t *testing.T) {
	// RFC 4231 Test Case 2
	key, _ := hex.DecodeString("4a656665")
	data := []byte("what do ya want for nothing?")
	got := hmacSHA256(key, data)
	want := "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
	if hex.EncodeToString(got) != want {
		t.Fatalf("hmacSHA256 = %x, want %s", got, want)
	}
}

func TestDeriveKey(t *testing.T) {
	// AWS SigV4 test vector
	// https://docs.aws.amazon.com/general/latest/gr/signature-v4-test-suite.html
	key := deriveKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20151229", "us-east-1", "s3")
	// Verify the key is deterministic and has the correct length (32 bytes).
	if len(key) != 32 {
		t.Fatalf("deriveKey length = %d, want 32", len(key))
	}
	// Verify determinism.
	key2 := deriveKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20151229", "us-east-1", "s3")
	if hex.EncodeToString(key) != hex.EncodeToString(key2) {
		t.Fatal("deriveKey is not deterministic")
	}
}

func TestNewR2(t *testing.T) {
	r := New("account123", "my-bucket", "access-key", "secret-key", "https://cdn.example.com")
	if r.AccountID != "account123" {
		t.Fatalf("got AccountID = %q", r.AccountID)
	}
	if r.Bucket != "my-bucket" {
		t.Fatalf("got Bucket = %q", r.Bucket)
	}
	if r.PublicBase != "https://cdn.example.com" {
		t.Fatalf("got PublicBase = %q", r.PublicBase)
	}
	if r.HTTP == nil {
		t.Fatal("HTTP client should not be nil")
	}
}
