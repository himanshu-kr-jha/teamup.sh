package org

// Somebody's own profile picture: set by them, seen by their organization,
// served at an address that changes when the picture does.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSomebodySetsTheirOwnPictureAndColleaguesSeeIt(t *testing.T) {
	w := newMailWorld(t)
	me := w.as(t, "member") // signed in as Ada, an ordinary member here
	pic := tinyPNG(t)
	path := "/api/users/" + w.admin.ID.String() + "/avatar"

	set := send(t, me, http.MethodPut, path, map[string]string{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pic)})
	var body struct {
		AvatarURL string `json:"avatarUrl"`
	}
	_ = json.Unmarshal(set.Body.Bytes(), &body)
	if set.Code != http.StatusOK || !strings.HasPrefix(body.AvatarURL, path+"?v=") {
		t.Fatalf("set: %d %s", set.Code, set.Body.String())
	}
	u, err := w.svc.GetUser(context.Background(), w.admin.ID)
	if err != nil || u.AvatarURL == nil || *u.AvatarURL != body.AvatarURL {
		t.Fatalf("the profile points at it: %+v %v", u, err)
	}

	got := send(t, me, http.MethodGet, body.AvatarURL, nil)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" || !bytes.Equal(got.Body.Bytes(), pic) {
		t.Fatalf("served: %d %v", got.Code, got.Header())
	}
	if !strings.Contains(got.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("its own address is cached for good: %v", got.Header())
	}

	if del := send(t, me, http.MethodDelete, path, nil); del.Code != http.StatusNoContent {
		t.Fatalf("remove: %d", del.Code)
	}
	if got := send(t, me, http.MethodGet, path, nil); got.Code != http.StatusNotFound {
		t.Errorf("gone: %d", got.Code)
	}
	if u, _ := w.svc.GetUser(context.Background(), w.admin.ID); u.AvatarURL != nil {
		t.Errorf("back to initials: %v", *u.AvatarURL)
	}
}

func TestNobodyChangesSomebodyElsesPicture(t *testing.T) {
	w := newMailWorld(t)
	other, _, err := w.svc.AddMember(context.Background(), w.org, "Bo", "bo@mail.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	img := map[string]string{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG(t))}
	if got := send(t, w.as(t, "member"), http.MethodPut, "/api/users/"+other.ID.String()+"/avatar", img); got.Code != http.StatusForbidden {
		t.Errorf("a member: %d", got.Code)
	}
	if got := send(t, w.as(t, "admin"), http.MethodPut, "/api/users/"+other.ID.String()+"/avatar", img); got.Code != http.StatusOK {
		t.Errorf("an administrator may: %d", got.Code)
	}
}

func TestOnlyPicturesAreProfilePictures(t *testing.T) {
	w := newMailWorld(t)
	path := "/api/users/" + w.admin.ID.String() + "/avatar"
	for name, image := range map[string]string{
		"svg":     "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
		"text":    "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("hello")),
		"a link":  "https://example.com/me.png",
		"too big": "data:image/png;base64," + base64.StdEncoding.EncodeToString(append(tinyPNG(t), make([]byte, MaxAvatarBytes)...)),
	} {
		if got := send(t, w.as(t, "member"), http.MethodPut, path, map[string]string{"image": image}); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, got.Code)
		}
	}
}
