package botapi

import (
	"encoding/json"
	"strings"
	"testing"

	"telesrv/internal/domain"
)

func TestGameAndPassportUpdatesProjectOnlyRecipientData(t *testing.T) {
	game := domain.Message{ID: 1, OwnerUserID: 20, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 10}, Media: &domain.MessageMedia{Kind: domain.MessageMediaKindGame, Game: &domain.BotGame{ID: 3, BotUserID: 10, Title: "Race"}}}
	if update, kind, ok := apiUpdate(domain.UpdateEvent{Type: domain.UpdateEventNewMessage, Pts: 1, Message: game}); !ok || kind != "message" || update["message"].(map[string]any)["game"] == nil {
		t.Fatalf("empty-body game update dropped: %#v %q %v", update, kind, ok)
	}
	authorization := &domain.PassportAuthorization{BotUserID: 10, Elements: []domain.PassportElement{{Type: "personal_details", Data: "encrypted-data", Hash: "element-hash", SecretID: 9988, ValueTL: []byte("private-secure-value-wire")}}, CredentialsData: []byte("encrypted-credentials"), CredentialsHash: []byte("credential-hash"), CredentialsSecret: []byte("encrypted-credential-secret")}
	message := domain.Message{ID: 2, OwnerUserID: 10, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 20}, Media: &domain.MessageMedia{Kind: domain.MessageMediaKindService, ServiceAction: &domain.MessageServiceAction{Kind: domain.MessageServiceActionPassportData, PassportData: authorization}}}
	update, kind, ok := apiUpdate(domain.UpdateEvent{Type: domain.UpdateEventNewMessage, Pts: 2, Message: message})
	if !ok || kind != "message" {
		t.Fatalf("received Passport update dropped: %#v %q %v", update, kind, ok)
	}
	projected := update["message"].(map[string]any)["passport_data"].(map[string]any)
	element := projected["data"].([]map[string]any)[0]
	if element["data"] != "encrypted-data" || element["hash"] != "element-hash" || projected["credentials"] == nil {
		t.Fatalf("missing encrypted payload: %#v", projected)
	}
	wire, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "private-secure-value-wire") || strings.Contains(string(wire), "9988") {
		t.Fatal("Passport internal secure value or secret ID exposed")
	}
	message.OwnerUserID = 20
	message.Out = true
	if _, _, ok := apiUpdate(domain.UpdateEvent{Type: domain.UpdateEventNewMessage, Pts: 2, Message: message}); ok {
		t.Fatal("sender's Passport copy emitted as received data")
	}
	message.Out = false
	if _, _, ok := apiUpdate(domain.UpdateEvent{Type: domain.UpdateEventNewMessage, Pts: 2, Message: message}); ok {
		t.Fatal("non-bot owner received another user's Passport data")
	}
}
