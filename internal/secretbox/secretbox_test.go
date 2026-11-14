package secretbox

import (
	"bytes"
	"strings"
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
	// Every token, not just this second's: a changed last character used to read
	// back the same about once in a thousand, because its spare bits were ignored.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := 1; i <= 200; i++ {
		tok, _ := b.Token(map[string]string{"user": "u1"}, time.Duration(i)*time.Second)
		last := strings.IndexByte(alphabet, tok[len(tok)-1])
		// The three characters that differ from it in the spare bits alone.
		for spare := 1; spare <= 3; spare++ {
			changed := tok[:len(tok)-1] + string(alphabet[last^spare])
			if b.ReadToken(changed, &v) == nil {
				t.Fatalf("token %d with its last character changed in its spare bits was accepted", i)
			}
		}
	}
	old, _ := b.Token("x", -time.Second)
	if err := b.ReadToken(old, &v); err == nil {
		t.Error("an expired token is refused")
	}
}
