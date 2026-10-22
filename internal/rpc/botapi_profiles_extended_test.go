package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	appfiles "telesrv/internal/app/files"
	"telesrv/internal/botapi"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type profileHTTPResponse struct {
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
	Parameters struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func profileHTTPCall(t *testing.T, server http.Handler, token, method string, payload any) (profileHTTPResponse, int) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/"+method, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	var out profileHTTPResponse
	if err = json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %s %v", method, rr.Body, err)
	}
	return out, rr.Code
}
func profileHTTPServer(t *testing.T, f botAPIBridgeFixture) *http.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	t.Cleanup(cancel)
	s, err := botapi.Start(ctx, "127.0.0.1:0", f.bots, f.users, nil, nil, f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestBotAPIProfilesHTTPUploadPaginationClearAndRights(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	blobs, err := appfiles.NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := appfiles.NewService(memory.NewMediaStore(), blobs, 2)
	f.router.deps.Files = files
	server := profileHTTPServer(t, f)
	token := domain.FormatBotToken(f.bot.ID, "secret")
	upload := func() {
		t.Helper()
		var jpg bytes.Buffer
		if err := jpeg.Encode(&jpg, image.NewNRGBA(image.Rect(0, 0, 640, 480)), nil); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("photo", `{"type":"static","photo":"attach://avatar"}`); err != nil {
			t.Fatal(err)
		}
		part, err := form.CreateFormFile("avatar", "avatar.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(jpg.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/setMyProfilePhoto", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", form.FormDataContentType())
		rr := httptest.NewRecorder()
		server.Handler.ServeHTTP(rr, req)
		var res profileHTTPResponse
		if json.Unmarshal(rr.Body.Bytes(), &res) != nil || !res.OK {
			t.Fatalf("upload=%s", rr.Body)
		}
	}
	upload()
	upload()
	upload()
	got, status := profileHTTPCall(t, server.Handler, token, "getUserProfilePhotos", map[string]any{"user_id": f.bot.ID, "offset": 1, "limit": 1})
	var album struct {
		Total  int `json:"total_count"`
		Photos [][]struct {
			ID     string `json:"file_id"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"photos"`
	}
	if err = json.Unmarshal(got.Result, &album); err != nil {
		t.Fatal(err)
	}
	if status != 200 || !got.OK || album.Total != 3 || len(album.Photos) != 1 || len(album.Photos[0]) != 3 {
		t.Fatalf("album page=%s", got.Result)
	}
	dimensions := map[[2]int]bool{}
	for _, size := range album.Photos[0] {
		dimensions[[2]int{size.Width, size.Height}] = true
	}
	for _, want := range [][2]int{{150, 150}, {160, 160}, {640, 480}} {
		if !dimensions[want] {
			t.Fatalf("avatar renditions=%s", got.Result)
		}
	}
	// Historical ownership includes inactive rows. Observe the public active album
	// instead: removal must hide this photo and promote the preceding avatar.
	nextIDs := make(map[string]bool)
	for _, size := range album.Photos[0] {
		nextIDs[size.ID] = true
	}
	got, _ = profileHTTPCall(t, server.Handler, token, "getUserProfilePhotos", map[string]any{"user_id": f.bot.ID, "limit": 1})
	var active struct {
		Total  int `json:"total_count"`
		Photos [][]struct {
			ID string `json:"file_id"`
		} `json:"photos"`
	}
	if err = json.Unmarshal(got.Result, &active); err != nil || !got.OK || active.Total != 3 || len(active.Photos) != 1 {
		t.Fatalf("current album=%s err=%v", got.Result, err)
	}
	removedIDs := make(map[string]bool)
	for _, size := range active.Photos[0] {
		removedIDs[size.ID] = true
	}
	got, _ = profileHTTPCall(t, server.Handler, token, "removeMyProfilePhoto", map[string]any{})
	if !got.OK {
		t.Fatalf("remove=%s", got.Result)
	}
	got, _ = profileHTTPCall(t, server.Handler, token, "getUserProfilePhotos", map[string]any{"user_id": f.bot.ID})
	if err = json.Unmarshal(got.Result, &active); err != nil || !got.OK || active.Total != 2 || len(active.Photos) != 2 || len(active.Photos[0]) != len(nextIDs) {
		t.Fatalf("remaining album=%s err=%v", got.Result, err)
	}
	for index, photo := range active.Photos {
		for _, size := range photo {
			if removedIDs[size.ID] {
				t.Fatalf("removed current avatar still visible: %s", got.Result)
			}
			if index == 0 && !nextIDs[size.ID] {
				t.Fatalf("preceding avatar not promoted: %s", got.Result)
			}
		}
	}
	set := func(channels bool, rights any) {
		t.Helper()
		out, _ := profileHTTPCall(t, server.Handler, token, "setMyDefaultAdministratorRights", map[string]any{"for_channels": channels, "rights": rights})
		if !out.OK {
			t.Fatalf("set rights=%s", out.Result)
		}
	}
	set(false, map[string]bool{"can_delete_messages": true})
	set(true, map[string]bool{"can_post_messages": true})
	for _, channels := range []bool{false, true} {
		out, _ := profileHTTPCall(t, server.Handler, token, "getMyDefaultAdministratorRights", map[string]any{"for_channels": channels})
		var rights map[string]bool
		if err = json.Unmarshal(out.Result, &rights); err != nil {
			t.Fatal(err)
		}
		if rights["can_delete_messages"] == channels || rights["can_post_messages"] != channels {
			t.Fatalf("scope=%v rights=%v", channels, rights)
		}
	}
	got, _ = profileHTTPCall(t, server.Handler, token, "setMyDefaultAdministratorRights", map[string]any{})
	if !got.OK {
		t.Fatal("clear rights failed")
	}
	got, _ = profileHTTPCall(t, server.Handler, token, "getMyDefaultAdministratorRights", map[string]any{})
	var cleared map[string]bool
	if err = json.Unmarshal(got.Result, &cleared); err != nil {
		t.Fatal(err)
	}
	for key, value := range cleared {
		if value {
			t.Fatalf("cleared %s still true", key)
		}
	}
}
func TestBotAPIProfileFallbackPaginationAndAudioPrivacy(t *testing.T) {
	f := newAlbumPrivacyFixture(t)
	fallback := f.setFallback()
	ctx := context.Background()
	photos, total, err := f.r.BotAPIGetUserProfilePhotos(ctx, f.bot.ID, f.owner.ID, 0, 1)
	if err != nil || total != 1 || len(photos) != 1 || photos[0].ID != fallback.ID {
		t.Fatalf("hidden album=%+v total=%d err=%v", photos, total, err)
	}
	photos, total, err = f.r.BotAPIGetUserProfilePhotos(ctx, f.bot.ID, f.owner.ID, 1, 1)
	if err != nil || total != 1 || len(photos) != 0 {
		t.Fatalf("fallback page leaks album=%+v total=%d err=%v", photos, total, err)
	}
	doc := domain.Document{ID: 98441, MimeType: "audio/mpeg", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrAudio, AudioDuration: 12, Title: "Private song"}}}
	if _, err = f.r.deps.Account.SaveMusic(ctx, f.owner.ID, domain.SaveMusicRequest{Document: doc, Date: 1800000000}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.deps.Privacy.SetRules(ctx, f.owner.ID, domain.PrivacyKeySavedMusic, []domain.PrivacyRule{{Kind: domain.PrivacyRuleDisallowAll}}); err != nil {
		t.Fatal(err)
	}
	list, err := f.r.BotAPIGetUserProfileAudios(ctx, f.bot.ID, f.owner.ID, 0, 100)
	if err != nil || list.Count != 0 || len(list.Documents) != 0 {
		t.Fatalf("hidden audio leaked=%+v %v", list, err)
	}
	if _, err = f.r.deps.Privacy.SetRules(ctx, f.owner.ID, domain.PrivacyKeySavedMusic, []domain.PrivacyRule{{Kind: domain.PrivacyRuleAllowAll}}); err != nil {
		t.Fatal(err)
	}
	list, err = f.r.BotAPIGetUserProfileAudios(ctx, f.bot.ID, f.owner.ID, 0, 100)
	if err != nil || list.Count != 1 || len(list.Documents) != 1 || list.Documents[0].ID != doc.ID {
		t.Fatalf("visible audio=%+v %v", list, err)
	}
	_, _, err = f.r.BotAPIGetUserProfilePhotos(ctx, f.bot.ID, f.owner.ID, 0, 101)
	if err == nil {
		t.Fatal("oversized album page accepted")
	}
}
