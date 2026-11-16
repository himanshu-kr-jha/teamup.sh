package files

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The live site's storage is Amazon S3 in Mumbai. minio-go signs uploads there
// for the dualstack host, not the configured one, and a Content-Security-Policy
// built from the configured host made the browser refuse every upload. The
// origin the policy is given has to be the one a real upload URL points at.
func TestThePublicOriginIsWhereUploadsReallyGo(t *testing.T) {
	for _, c := range []struct {
		name     string
		cfg      Config
		wantHost string
	}{
		{"Amazon, as on the live site", Config{Endpoint: "s3.ap-south-1.amazonaws.com", Secure: true, Region: "ap-south-1", Bucket: "teamup-files", AccessKey: "AKIAEXAMPLE", SecretKey: "example"}, ""},
		{"local MinIO, as in development", Config{Endpoint: "localhost:9000", Secure: false, Region: "us-east-1", Bucket: "teamup", AccessKey: "minio", SecretKey: "minio123"}, "localhost:9000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := NewService(c.cfg)
			origin := s.PublicOrigin()

			// An upload URL exactly as the browser is given one.
			signed, err := s.presigner.PresignHeader(context.Background(), http.MethodPut, c.cfg.Bucket, "u/file.pdf", time.Minute, nil, http.Header{})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := url.Parse(origin)
			if got == nil || got.Scheme != signed.Scheme || got.Host != signed.Host {
				t.Fatalf("the policy would allow %q but uploads go to %s://%s", origin, signed.Scheme, signed.Host)
			}
			if c.wantHost != "" && got.Host != c.wantHost {
				t.Errorf("local storage is reached where it is configured, got %q", origin)
			}
		})
	}
}
