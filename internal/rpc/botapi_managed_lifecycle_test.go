package rpc

import (
	"encoding/json"
	"telesrv/internal/domain"
	"testing"
)

func TestBotAPIManagedHTTPRejectsOtherManagersAndOldToken(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	if err := f.bots.SetCanManageBots(f.ctx, f.owner.ID, f.bot.ID, true); err != nil {
		t.Fatal(err)
	}
	child, oldToken, err := f.bots.CreateManagedBot(f.ctx, f.owner.ID, f.bot.ID, "Managed Child", "http_managed_child_bot")
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := f.bots.CreateBot(f.ctx, f.owner.ID, "Other", "http_other_manager_bot")
	if err != nil {
		t.Fatal(err)
	}
	f.bots.SetRouterHooks(f.router)
	server := profileHTTPServer(t, f)
	managerToken := domain.FormatBotToken(f.bot.ID, "secret")
	denied, status := profileHTTPCall(t, server.Handler, otherToken, "getManagedBotToken", map[string]any{"user_id": child.ID})
	if denied.OK || status != 400 {
		t.Fatalf("other manager access=%v %d", denied, status)
	}
	got, _ := profileHTTPCall(t, server.Handler, managerToken, "getManagedBotToken", map[string]any{"user_id": child.ID})
	var exported string
	if err = json.Unmarshal(got.Result, &exported); err != nil || exported != oldToken {
		t.Fatalf("exported=%s %v", got.Result, err)
	}
	got, _ = profileHTTPCall(t, server.Handler, managerToken, "setManagedBotAccessSettings", map[string]any{"user_id": child.ID, "is_access_restricted": true, "added_user_ids": []int64{f.member.ID}})
	if !got.OK {
		t.Fatalf("set access=%s", got.Result)
	}
	got, _ = profileHTTPCall(t, server.Handler, managerToken, "getManagedBotAccessSettings", map[string]any{"user_id": child.ID})
	var settings struct {
		Restricted bool `json:"is_access_restricted"`
		Added      []struct {
			ID int64 `json:"id"`
		} `json:"added_users"`
	}
	if err = json.Unmarshal(got.Result, &settings); err != nil || !settings.Restricted || len(settings.Added) != 1 || settings.Added[0].ID != f.member.ID {
		t.Fatalf("settings=%s %v", got.Result, err)
	}
	got, _ = profileHTTPCall(t, server.Handler, managerToken, "replaceManagedBotToken", map[string]any{"user_id": child.ID})
	var replacement string
	if err = json.Unmarshal(got.Result, &replacement); err != nil || replacement == oldToken || replacement == "" {
		t.Fatalf("replacement=%s %v", got.Result, err)
	}
	old, status := profileHTTPCall(t, server.Handler, oldToken, "getMe", map[string]any{})
	if old.OK || status != 401 {
		t.Fatalf("old credential survived=%v %d", old, status)
	}
	fresh, status := profileHTTPCall(t, server.Handler, replacement, "getMe", map[string]any{})
	if !fresh.OK || status != 200 {
		t.Fatalf("new credential unusable=%v %d", fresh, status)
	}
}

func TestBotAPIInstanceHTTPStopsAndLocalRestartWorks(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	server := profileHTTPServer(t, f)
	token := domain.FormatBotToken(f.bot.ID, "secret")
	early, status := profileHTTPCall(t, server.Handler, token, "close", map[string]any{})
	if early.OK || status != 429 || early.Parameters.RetryAfter < 599 {
		t.Fatalf("early close=%+v status=%d", early, status)
	}
	out, status := profileHTTPCall(t, server.Handler, token, "logOut", map[string]any{})
	if !out.OK || status != 200 {
		t.Fatalf("logout=%+v %d", out, status)
	}
	closed, status := profileHTTPCall(t, server.Handler, token, "getMe", map[string]any{})
	if closed.OK || status != 409 {
		t.Fatalf("closed instance accepted request=%+v %d", closed, status)
	}
	restarted := profileHTTPServer(t, f)
	out, status = profileHTTPCall(t, restarted.Handler, token, "getMe", map[string]any{})
	if !out.OK || status != 200 {
		t.Fatalf("local restart=%+v %d", out, status)
	}
}
