package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telesrv/internal/botapi"
	"telesrv/internal/domain"
)

func suggestedPostHTTP(t *testing.T, f botAPIBridgeFixture) func(string, map[string]any, bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	server, err := botapi.Start(ctx, "127.0.0.1:0", f.bots, f.users, nil, nil, f.router, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	return func(method string, params map[string]any, wantOK bool) {
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
			OK     bool `json:"ok"`
			Result bool `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.OK != wantOK || (wantOK && !response.Result) {
			t.Fatalf("%s response=%s err=%v wantOK=%v", method, rec.Body.String(), err, wantOK)
		}
	}
}

func TestBotAPISuggestedPostHTTPRealLifecycle(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	created, err := f.channels.CreateChannel(f.ctx, f.owner.ID, domain.CreateChannelRequest{Title: "Suggested channel", Broadcast: true, Date: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := f.channels.SetPaidMessagesPrice(f.ctx, f.owner.ID, created.Channel.ID, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	monoID := enabled.Channel.LinkedMonoforumID
	chatID := -botAPIChannelChatIDBase - monoID
	send := func(randomID int64) domain.SendChannelMessageResult {
		t.Helper()
		res, err := f.channels.SendMonoforumMessage(f.ctx, domain.SendMonoforumMessageRequest{MonoforumID: monoID, SenderUserID: f.member.ID, SavedPeer: domain.Peer{Type: domain.PeerTypeUser, ID: f.member.ID}, RandomID: randomID, Message: "A real suggestion", SuggestedPost: &domain.SuggestedPost{}, Date: 1700000100})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	first := send(8701)
	call := suggestedPostHTTP(t, f)
	params := map[string]any{"chat_id": chatID, "message_id": first.Message.ID}
	call("approveSuggestedPost", params, false)
	if _, err := f.channels.EditAdmin(f.ctx, f.owner.ID, domain.EditChannelAdminRequest{ChannelID: created.Channel.ID, MemberID: f.bot.ID, AdminRights: domain.ChannelAdminRights{ManageDirectMessages: true, PostMessages: true}, Date: 1700000101}); err != nil {
		t.Fatal(err)
	}
	call("approveSuggestedPost", params, true)
	call("approveSuggestedPost", params, true)
	call("declineSuggestedPost", params, false)
	second := send(8702)
	params = map[string]any{"chat_id": chatID, "message_id": second.Message.ID, "comment": "Not for this channel"}
	call("declineSuggestedPost", params, true)
	call("declineSuggestedPost", params, true)
	call("approveSuggestedPost", params, false)
	history, err := f.channels.GetMessages(f.ctx, f.bot.ID, monoID, []int{first.Message.ID, second.Message.ID})
	if err != nil {
		t.Fatal(err)
	}
	approved, declined := false, false
	for _, msg := range history.Messages {
		if msg.ID == first.Message.ID {
			approved = msg.SuggestedPost != nil && msg.SuggestedPost.Accepted
		}
		if msg.ID == second.Message.ID {
			declined = msg.SuggestedPost != nil && msg.SuggestedPost.Rejected
		}
	}
	if !approved || !declined {
		t.Fatalf("approval/decline not persisted: %+v", history.Messages)
	}
}
