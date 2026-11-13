package notifications

import "testing"

func TestPushGoesOnlyToBrowserPushServices(t *testing.T) {
	var c Config
	for endpoint, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGx":                         true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":    true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false,
		"http://minio:9000/teamup-files/x":                       false,
		"http://169.254.169.254/latest/meta-data/":               false,
		"https://127.0.0.1/phone":                                false,
		"https://fcm.googleapis.com.evil.example/x":              false,
		"https://evilpush.services.mozilla.com.example/x":        false,
		"https://user@fcm.googleapis.com/x":                      false,
		"not a url":                                              false,
	} {
		if got := c.pushEndpointAllowed(endpoint); got != want {
			t.Errorf("%s: got %v, want %v", endpoint, got, want)
		}
	}
	// A host named on purpose, as a test's fake phone is, may be plain HTTP.
	c.PushServiceHosts = []string{"127.0.0.1"}
	if !c.pushEndpointAllowed("http://127.0.0.1:8099/phone") {
		t.Error("the configured host is refused")
	}
	if c.pushEndpointAllowed("https://fcm.googleapis.com/x") {
		t.Error("a configured list replaces the default, so nothing else is allowed")
	}
}
