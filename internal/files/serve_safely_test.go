package files

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

// uploadAs puts a file in storage the way a browser does, with whatever
// Content-Type the uploader chooses to send.
func uploadAs(t *testing.T, svc *Service, req UploadRequest, body []byte, sentType string) string {
	t.Helper()
	req.Size = int64(len(body))
	up, err := svc.GenerateUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("upload url: %v", err)
	}
	put, err := http.NewRequest(http.MethodPut, up.UploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	put.ContentLength = int64(len(body))
	put.Header.Set("Content-Type", sentType)
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}
	return up.ObjectKey
}

func served(t *testing.T, rawurl string) (contentType, disposition string) {
	t.Helper()
	resp, err := http.Get(rawurl)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	return resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition")
}

// Files are served from the app's own address, so a page somebody uploads and
// then links to would run as whoever opens the link: with their session, able
// to do anything they can. Whatever the uploader claimed, only pictures,
// sound, video and PDFs are ever shown in place; everything else downloads,
// and never as a page.
func TestLiveAnUploadedPageIsNeverServedAsAPage(t *testing.T) {
	svc, _ := liveService(t)
	ctx := context.Background()
	req, orgID, userID := liveChatUpload(t, svc, 1)

	for _, tc := range []struct{ name, filename, sentType string }{
		{"a page", "x.html", "text/html"},
		{"a drawing that can carry script", "x.svg", "image/svg+xml"},
		{"a page named as a picture", "x.png", "text/html"},
		{"a page with no name to go by", "x", "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			r.Filename = tc.filename
			key := uploadAs(t, svc, r, []byte("<script>alert(document.domain)</script>"), tc.sentType)
			for _, inline := range []bool{true, false} {
				link, err := svc.GenerateDownloadURL(ctx, orgID.String(), userID.String(), key, tc.filename, inline)
				if err != nil {
					t.Fatalf("download url: %v", err)
				}
				ct, cd := served(t, link)
				if strings.Contains(ct, "html") || strings.Contains(ct, "svg") || strings.Contains(ct, "xml") {
					t.Errorf("inline=%v: served as %q", inline, ct)
				}
				if strings.HasPrefix(cd, "inline") && !strings.HasPrefix(ct, "image/") {
					t.Errorf("inline=%v: shown in place as %q (%s)", inline, ct, cd)
				}
			}
		})
	}

	// A picture is still shown in place, as the chat shows it.
	r := req
	r.Filename = "photo.png"
	key := uploadAs(t, svc, r, []byte("\x89PNG\r\n\x1a\n"), "image/png")
	link, err := svc.GenerateDownloadURL(ctx, orgID.String(), userID.String(), key, "photo.png", true)
	if err != nil {
		t.Fatal(err)
	}
	if ct, cd := served(t, link); ct != "image/png" || !strings.HasPrefix(cd, "inline") {
		t.Errorf("a picture: served as %q, %q", ct, cd)
	}
}
