package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/tgerr"

	"telesrv/internal/domain"
)

func TestBotAccessGateReadsLiveSettings(t *testing.T) {
	ctx := context.Background()
	router, bots, users, owner, bot := newBotRPCTestRouter(t)
	visitor, err := users.Create(ctx, domain.User{Phone: "15550009811", FirstName: "Visitor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bots.SetBotAccessSettings(ctx, owner.ID, bot.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := router.ensureBotAccess(ctx, bot.ID, visitor.ID); !tgerr.Is(err, "BOT_ACCESS_DENIED") {
		t.Fatalf("restricted visitor access = %v", err)
	}
	if err := router.ensureBotAccess(ctx, bot.ID, owner.ID); err != nil {
		t.Fatalf("owner access: %v", err)
	}
	if err := bots.SetBotAccessSettings(ctx, owner.ID, bot.ID, true, []int64{visitor.ID}); err != nil {
		t.Fatal(err)
	}
	if err := router.ensureBotAccess(ctx, bot.ID, visitor.ID); err != nil {
		t.Fatalf("allowlisted visitor access: %v", err)
	}
	if err := bots.SetBotAccessSettings(ctx, owner.ID, bot.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := router.ensureBotAccess(ctx, bot.ID, visitor.ID); !tgerr.Is(err, "BOT_ACCESS_DENIED") {
		t.Fatalf("revoked visitor access = %v", err)
	}
}
