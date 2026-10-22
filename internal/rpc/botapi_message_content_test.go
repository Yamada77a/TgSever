package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iamxvbaba/td/tg"
	"telesrv/internal/botapi"
	"telesrv/internal/domain"
)

func TestBotAPIExtendedMessagesHTTPStateTransitions(t *testing.T) {
	f, _, connection := newBusinessFixture(t, domain.BusinessBotRights{Reply: true})
	f.promoteBot(t, domain.ChannelAdminRights{DeleteMessages: true})
	now := int(f.router.clock.Now().Unix())
	if _, err := f.messages.SendPrivateText(f.ctx, f.member.ID, domain.SendPrivateTextRequest{SenderUserID: f.member.ID, RecipientUserID: f.owner.ID, RandomID: 87001, Message: "start business", Date: now}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	server, err := botapi.Start(ctx, "127.0.0.1:0", f.bots, f.users, nil, nil, f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	call := func(method string, fields map[string]any, want bool) map[string]any {
		t.Helper()
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bot%d:secret/%s", f.bot.ID, method), strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["ok"] != want {
			t.Fatalf("%s=%s", method, response.Body.String())
		}
		return result
	}
	sent, err := f.router.BotAPISendMessage(f.ctx, f.bot.ID, f.chatID, "before", nil, nil, false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := f.router.BotAPIEditMessageText(f.ctx, f.bot.ID, f.chatID, sent.ID, "after", nil, false, nil, false)
	if err != nil || edited.Body != "after" {
		t.Fatalf("group edit=%+v %v", edited, err)
	}
	call("deleteMessages", map[string]any{"chat_id": f.chatID, "message_ids": []int{sent.ID, 999999}}, true)
	history, err := f.channels.GetMessages(f.ctx, f.bot.ID, f.channel.ID, []int{sent.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range history.Messages {
		if !msg.Deleted {
			t.Fatalf("bulk delete not persisted: %+v", msg)
		}
	}
	live, err := f.router.BotAPISendOrdinary(f.ctx, domain.BotAPIEphemeralSendInput{BotUserID: f.bot.ID, ChatID: f.member.ID, Kind: "location", DirectMedia: &domain.MessageMedia{Kind: domain.MessageMediaKindGeoLive, GeoLive: &domain.MessageGeoLive{Geo: domain.MessageGeoPoint{Lat: 1, Long: 2}, Period: 900}}}, domain.BotAPIMessageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call("editMessageLiveLocation", map[string]any{"chat_id": f.member.ID, "message_id": live.ID, "latitude": 3, "longitude": 4}, true)
	call("stopMessageLiveLocation", map[string]any{"chat_id": f.member.ID, "message_id": live.ID}, true)
	call("editMessageLiveLocation", map[string]any{"chat_id": f.member.ID, "message_id": live.ID, "latitude": 5, "longitude": 6}, false)
	checklist := map[string]any{"title": "Shopping", "others_can_mark_tasks_as_done": true, "tasks": []any{map[string]any{"id": 7, "text": "Milk"}}}
	result := call("sendChecklist", map[string]any{"business_connection_id": connection.ConnectionID, "chat_id": f.member.ID, "checklist": checklist}, true)
	object := result["result"].(map[string]any)
	id := int(object["message_id"].(float64))
	if _, err := f.router.onMessagesToggleTodoCompleted(WithUserID(f.ctx, f.owner.ID), &tg.MessagesToggleTodoCompletedRequest{Peer: &tg.InputPeerUser{UserID: f.member.ID, AccessHash: f.member.AccessHash}, MsgID: id, Completed: []int{7}}); err != nil {
		t.Fatal(err)
	}
	checklist["title"] = "Updated"
	call("editMessageChecklist", map[string]any{"business_connection_id": connection.ConnectionID, "chat_id": f.member.ID, "message_id": id, "checklist": checklist}, true)
	stored, err := f.router.botAPIReloadMessage(f.ctx, f.owner.ID, domain.Peer{Type: domain.PeerTypeUser, ID: f.member.ID}, id)
	if err != nil || stored.Media.Todo.Title != "Updated" || len(stored.Media.Todo.Completions) != 1 || stored.Media.Todo.Completions[0].CompletedBy != f.owner.ID {
		t.Fatalf("checklist completion lost=%+v %v", stored, err)
	}
}
