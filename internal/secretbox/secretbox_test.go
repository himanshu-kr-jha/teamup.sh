package secretbox

import (
	"bytes"
	"testing"
	"time"
)

func TestSealedValuesOpenOnlyWithTheSameSecretAndPurpose(t *testing.T) {
	a, err := New("a-server-secret-long-enough", "github")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal(map[string]string{"key": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("private")) {
		t.Fatal("sealed means unreadable")
	}
	var got map[string]string
	if err := a.Open(sealed, &got); err != nil || got["key"] != "private" {
		t.Fatalf("opens again: %v %v", got, err)
	}
	other, _ := New("a-server-secret-long-enough", "bitbucket")
	if err := other.Open(sealed, &got); err == nil {
		t.Error("another purpose cannot open it")
	}
	if _, err := New("short", "github"); err != ErrNoSecret {
		t.Error("a missing or short secret is refused")
	}
}

func TestTokensComeBackUnchangedAndInTime(t *testing.T) {
	b, _ := New("a-server-secret-long-enough", "github")
	tok, _ := b.Token(map[string]string{"user": "u1"}, time.Minute)
	var v map[string]string
	if err := b.ReadToken(tok, &v); err != nil || v["user"] != "u1" {
		t.Fatalf("reads back: %v %v", v, err)
	}
	if err := b.ReadToken(tok[:len(tok)-2]+"xx", &v); err == nil {
		t.Error("a changed token is refused")
	}
	old, _ := b.Token("x", -time.Second)
	if err := b.ReadToken(old, &v); err == nil {
		t.Error("an expired token is refused")
	}
}
