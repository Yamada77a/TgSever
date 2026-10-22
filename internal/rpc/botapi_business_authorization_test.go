package rpc

import (
	"context"
	"fmt"
	"testing"

	accountapp "telesrv/internal/app/account"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestBotAPIBusinessAuthorizationCore(t *testing.T) {
	ctx := context.Background()
	const ownerID, botID, peerID, otherBotID int64 = 1000000201, 1000000202, 1000000203, 1000000204
	passwords := memory.NewPasswordStore()
	service := accountapp.NewService(passwords, accountapp.WithBusinessAutomation(passwords))
	r := &Router{deps: Deps{
		Account: service,
		Users: mapUsersService{users: map[int64]domain.User{
			ownerID:    {ID: ownerID},
			botID:      {ID: botID, Bot: true},
			peerID:     {ID: peerID},
			otherBotID: {ID: otherBotID, Bot: true},
		}},
	}}
	connection, err := service.SaveConnectedBusinessBot(ctx, ownerID, domain.ConnectedBusinessBot{
		BotUserID:  botID,
		Recipients: domain.BusinessBotRecipients{ExcludeSelected: true},
		Rights:     domain.BusinessBotRights{EditName: true, Reply: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := r.botAPIBusinessOwner(ctx, botID, connection.ConnectionID, "can_edit_name"); err != nil || owner != ownerID {
		t.Fatalf("delegated owner = %d, %v", owner, err)
	}
	for _, request := range []struct {
		bot        int64
		connection string
		right      string
	}{
		{otherBotID, connection.ConnectionID, "can_edit_name"},
		{peerID, connection.ConnectionID, "can_edit_name"},
		{botID, fmt.Sprint(ownerID), "can_edit_name"},
		{botID, connection.ConnectionID, "can_edit_bio"},
	} {
		if _, err := r.botAPIBusinessOwner(ctx, request.bot, request.connection, request.right); err == nil {
			t.Fatalf("invalid delegation accepted: %+v", request)
		}
	}
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: peerID}
	if owner, err := r.botAPIBusinessPeer(ctx, botID, connection.ConnectionID, "", peer); err != nil || owner != ownerID {
		t.Fatalf("eligible peer owner = %d, %v", owner, err)
	}
	if _, err := service.SetConnectedBusinessBotPaused(ctx, ownerID, peerID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.botAPIBusinessPeer(ctx, botID, connection.ConnectionID, "", peer); err == nil {
		t.Fatal("paused peer authorized")
	}
	if _, err := service.SetConnectedBusinessBotPaused(ctx, ownerID, peerID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.botAPIBusinessPeer(ctx, botID, connection.ConnectionID, "can_reply", peer); err == nil {
		t.Fatal("reply authorized without activity storage")
	}
	for _, recipients := range []domain.BusinessBotRecipients{
		{ExcludeSelected: true, ExcludeUsers: []int64{peerID}},
		{ExistingChats: true},
		{Contacts: true},
	} {
		connection.Recipients = recipients
		if _, err := service.SaveConnectedBusinessBot(ctx, ownerID, connection); err != nil {
			t.Fatal(err)
		}
		if _, err := r.botAPIBusinessPeer(ctx, botID, connection.ConnectionID, "", peer); err == nil {
			t.Fatalf("excluded or unresolvable recipient authorized: %+v", recipients)
		}
	}
	if deleted, err := service.DeleteConnectedBusinessBot(ctx, ownerID, botID); err != nil || !deleted {
		t.Fatalf("revoke connection: %v, %v", deleted, err)
	}
	if _, err := r.botAPIBusinessOwner(ctx, botID, connection.ConnectionID, "can_edit_name"); err == nil {
		t.Fatal("revoked connection authorized")
	}
	r.deps.Account = accountapp.NewService(memory.NewPasswordStore())
	if _, err := r.botAPIBusinessOwner(ctx, botID, connection.ConnectionID, ""); err == nil {
		t.Fatal("unconfigured business store authorized")
	}
	r.deps.Account = nil
	if _, err := r.botAPIBusinessOwner(ctx, botID, connection.ConnectionID, ""); err == nil {
		t.Fatal("missing account service authorized")
	}
}
