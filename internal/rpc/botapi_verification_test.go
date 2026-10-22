package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appverification "telesrv/internal/app/botverification"
	"telesrv/internal/botapi"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func verificationHTTP(t *testing.T, f botAPIBridgeFixture) func(string, map[string]any, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	server, err := botapi.Start(ctx, "127.0.0.1:0", f.bots, f.users, nil, nil, f.router, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	return func(method string, params map[string]any, wantError string) {
		t.Helper()
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bot%d:secret/%s", f.bot.ID, method), strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)
		var response struct {
			OK          bool   `json:"ok"`
			Result      bool   `json:"result"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("%s response=%s err=%v", method, rec.Body.String(), err)
		}
		if wantError == "" {
			if rec.Code != http.StatusOK || !response.OK || !response.Result {
				t.Fatalf("%s response=%s status=%d", method, rec.Body.String(), rec.Code)
			}
		} else if rec.Code != http.StatusBadRequest || response.OK || response.Description != wantError {
			t.Fatalf("%s response=%s status=%d want=%s", method, rec.Body.String(), rec.Code, wantError)
		}
	}
}

func TestBotAPIVerificationHTTPRealMarks(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	marks := memory.NewBotVerificationStore()
	service := appverification.NewService(appverification.WithStore(marks), appverification.WithUserDirectory(f.users), appverification.WithBotDirectory(f.bots), appverification.WithChannelDirectory(f.channels))
	f.router.deps.BotVerifications = service
	call := verificationHTTP(t, f)
	call("verifyUser", map[string]any{"user_id": f.member.ID}, "BOT_VERIFIER_FORBIDDEN")
	settings := domain.BotVerifierSettings{BotID: f.bot.ID, IconDocumentID: 8801100, CompanyName: "Trust", DefaultDescription: "Trusted", CanModifyCustomDescription: true, Enabled: true}
	savedSettings, err := marks.UpsertBotVerifierSettings(f.ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	settings = savedSettings
	cases := []struct {
		verify, remove, key string
		id                  int64
		peer                domain.Peer
	}{
		{"verifyUser", "removeUserVerification", "user_id", f.member.ID, domain.Peer{Type: domain.PeerTypeUser, ID: f.member.ID}},
		{"verifyChat", "removeChatVerification", "chat_id", f.chatID, domain.Peer{Type: domain.PeerTypeChannel, ID: f.channel.ID}},
	}
	for _, tc := range cases {
		call(tc.verify, map[string]any{tc.key: tc.id, "custom_description": "Trusted shop"}, "")
		mark, err := service.PeerVerification(f.ctx, tc.peer)
		if err != nil || mark.VerifierBotID != f.bot.ID || mark.Description != "Trusted shop" {
			t.Fatalf("stored mark=%+v err=%v", mark, err)
		}
		call(tc.remove, map[string]any{tc.key: tc.id}, "")
		if _, err := service.PeerVerification(f.ctx, tc.peer); !errors.Is(err, domain.ErrCustomVerificationNotFound) {
			t.Fatalf("revoked mark read=%v", err)
		}
		call(tc.remove, map[string]any{tc.key: tc.id}, "")
	}
	call("verifyChat", map[string]any{"chat_id": "@BridgeBot"}, "")
	if mark, err := service.PeerVerification(f.ctx, domain.Peer{Type: domain.PeerTypeUser, ID: f.bot.ID}); err != nil || mark.VerifierBotID != f.bot.ID || mark.Description != "Trusted" {
		t.Fatalf("bot username mark=%+v err=%v", mark, err)
	}
	call("removeChatVerification", map[string]any{"chat_id": "@BridgeBot"}, "")
	call("verifyChat", map[string]any{"chat_id": "@ann_member"}, "CHAT_ID_INVALID")
	call("verifyChat", map[string]any{"chat_id": "@bridgegroup"}, "")
	call("removeChatVerification", map[string]any{"chat_id": "@bridgegroup"}, "")
	call("verifyUser", map[string]any{"user_id": f.member.ID, "custom_description": strings.Repeat("界", 71)}, "CUSTOM_DESCRIPTION_INVALID")
	settings.Enabled = false
	savedSettings, err = marks.UpsertBotVerifierSettings(f.ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	settings = savedSettings
	call("verifyUser", map[string]any{"user_id": f.member.ID}, "BOT_VERIFIER_FORBIDDEN")
	settings.Enabled = true
	savedSettings, err = marks.UpsertBotVerifierSettings(f.ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	settings = savedSettings
	foreign, _, err := f.bots.CreateBot(f.ctx, f.owner.ID, "Foreign verifier", "foreign_verify_bot")
	if err != nil {
		t.Fatal(err)
	}
	settings.BotID = foreign.ID
	settings.Version = 0
	if _, err := marks.UpsertBotVerifierSettings(f.ctx, settings); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if err := f.router.BotAPISetVerification(f.ctx, foreign.ID, tc.id, true, "Foreign trust"); err != nil {
			t.Fatal(err)
		}
		call(tc.remove, map[string]any{tc.key: tc.id}, "BOT_VERIFIER_FORBIDDEN")
		mark, err := service.PeerVerification(f.ctx, tc.peer)
		if err != nil || mark.VerifierBotID != foreign.ID || mark.Description != "Foreign trust" {
			t.Fatalf("foreign mark changed=%+v err=%v", mark, err)
		}
	}
	user, found, err := f.users.AdminUser(f.ctx, f.member.ID)
	if err != nil || !found || user.Verified {
		t.Fatalf("organization mark changed platform verification=%+v %v", user, err)
	}
}
