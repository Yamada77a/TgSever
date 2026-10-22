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

func TestBotAPIReactionMethodsHTTPStateTransitions(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	f.promoteBot(t, domain.ChannelAdminRights{DeleteMessages: true})
	now := int(f.router.clock.Now().Unix())
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
	id := f.sendAs(t, f.owner.ID, "reaction HTTP", 87001)
	call("setMessageReaction", map[string]any{"chat_id": f.chatID, "message_id": id, "reaction": []any{map[string]any{"type": "paid"}}}, false)
	call("setMessageReaction", map[string]any{"chat_id": f.chatID, "message_id": id, "reaction": []any{map[string]any{"type": "emoji", "emoji": "not an emoji"}}}, false)
	call("setMessageReaction", map[string]any{"chat_id": f.chatID, "message_id": id, "reaction": []any{map[string]any{"type": "emoji", "emoji": "👍"}}}, true)
	actor := domain.Peer{Type: domain.PeerTypeChannel, ID: f.channel.ID}
	addActor := func() {
		t.Helper()
		_, err := f.channels.SetMessageReactions(f.ctx, f.owner.ID, domain.SetChannelMessageReactionsRequest{Actor: actor, ChannelID: f.channel.ID, MessageID: id, Reactions: []domain.MessageReaction{{Type: domain.MessageReactionEmoji, Emoticon: "🔥"}}, Date: now})
		if err != nil {
			t.Fatal(err)
		}
	}
	addActor()
	call("deleteMessageReaction", map[string]any{"chat_id": f.chatID, "message_id": id, "actor_chat_id": f.chatID}, true)
	addActor()
	call("deleteAllMessageReactions", map[string]any{"chat_id": f.chatID, "actor_chat_id": f.chatID}, true)
	state, err := f.channels.GetMessageReactions(f.ctx, f.bot.ID, domain.ChannelMessageReactionsRequest{ChannelID: f.channel.ID, IDs: []int{id}})
	if err != nil || len(state.Reactions.Results) != 1 || state.Reactions.Results[0].Reaction.Emoticon != "👍" {
		t.Fatalf("moderation damaged bot reaction=%+v %v", state, err)
	}
}
