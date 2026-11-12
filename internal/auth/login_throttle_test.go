package auth

import (
	"net/http"
	"testing"
	"time"
)

// Guesses at one account are limited however many addresses they come from,
// and the right password then waits out the window too.
func TestAnAccountStopsTakingGuessesAfterTooManyWrongPasswords(t *testing.T) {
	b := newBrowser(t)
	for i := 0; i < loginFailuresAllowed; i++ {
		res := b.post("/api/auth/login", `{"email":"`+b.email+`","password":"wrong-password-`+string(rune('a'+i))+`"}`)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, res.StatusCode)
		}
	}
	res := b.post("/api/auth/login", `{"email":"`+b.email+`","password":"`+logoutTestPassword+`"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after %d wrong passwords: %d", loginFailuresAllowed, res.StatusCode)
	}
}

// An address with no account takes as long to refuse as a wrong password, so
// the time an answer takes does not tell anybody who has an account.
func TestAnUnknownAddressTakesAsLongAsAWrongPassword(t *testing.T) {
	b := newBrowser(t)
	timed := func(email string) time.Duration {
		start := time.Now()
		res := b.post("/api/auth/login", `{"email":"`+email+`","password":"not-the-password"}`)
		res.Body.Close()
		return time.Since(start)
	}
	timed("warm@up.example")
	known, unknown := timed(b.email), timed("nobody-here@example.test")
	if unknown < known/3 {
		t.Errorf("an unknown address answered in %v, a known one in %v", unknown, known)
	}
}
