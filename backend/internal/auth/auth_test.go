package auth

import (
	"bytes"
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrPassword {
		t.Fatalf("short password: got %v, want ErrPassword", err)
	}
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse") {
		t.Error("right password rejected")
	}
	if CheckPassword(hash, "wrong horse") {
		t.Error("wrong password accepted")
	}
	if CheckPassword("", "anything") {
		t.Error("unknown user accepted")
	}
}

func TestToken(t *testing.T) {
	a, hashA, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewToken()
	if a == b {
		t.Error("tokens repeat")
	}
	if !bytes.Equal(hashA, HashToken(a)) {
		t.Error("hash does not match token")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	l.Fail("k")
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("blocked after 1 failure")
	}
	l.Fail("k")
	if ok, wait := l.Allowed("k"); ok || wait != time.Minute {
		t.Fatalf("after 2 failures: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allowed("other"); !ok {
		t.Error("unrelated key blocked")
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.Allowed("k"); !ok {
		t.Error("still blocked after the window")
	}
	l.Fail("k")
	l.Reset("k")
	if len(l.failures) != 0 {
		t.Error("reset left entries behind")
	}
}
