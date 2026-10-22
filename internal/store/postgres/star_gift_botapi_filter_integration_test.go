package postgres

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestStarGiftBotAPIFilterBeforePaginationPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1885"+suffix+"01", "InventoryOwner", "")
	sender := createTestUser(t, ctx, users, "+1885"+suffix+"02", "InventorySender", "")
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	gifts, messages := NewStarGiftStore(pool), NewMessageStore(pool)
	base := time.Now().UnixNano() & 0x7ffffffffffff000
	catalog := func(giftID int64, limited bool, index int64) domain.StarGift {
		t.Helper()
		write := domain.StarGiftCatalogWrite{GiftID: giftID, Title: "Inventory " + suffix, Stars: 50, ConvertStars: 20, Enabled: true,
			Limited: limited, Document: collectibleTestDocument(base+index, "inventory.tgs"),
			Blob: collectibleTestBlob(base+index, "inventory"), Animation: collectibleTestAnimation("inventory.tgs"), Actor: "test"}
		if limited {
			write.AvailabilityTotal, write.AvailabilityRemains = 10, 10
		}
		entry, err := gifts.CreateCatalogRevision(ctx, write)
		if err != nil {
			t.Fatal(err)
		}
		return entry.Gift
	}
	limited, unlimited := catalog(0, true, 1), catalog(0, false, 2)
	receive := func(gift domain.StarGift, date int) domain.SavedStarGift {
		t.Helper()
		return createCollectibleSavedGift(t, ctx, messages, gifts, gift, domain.SavedStarGift{
			Owner: peer, FromUserID: sender.ID, GiftID: gift.ID, RevisionID: gift.RevisionID, Date: date, ConvertStars: 20,
		})
	}
	first, second := receive(limited, 10), receive(limited, 20)
	receive(unlimited, 30)
	if err := gifts.SetPinned(ctx, peer, []int64{first.ID}); err != nil {
		t.Fatal(err)
	}
	// The live catalog no longer advertises limited inventory, but received
	// gifts must still match the category of their immutable revision.
	catalog(limited.ID, false, 3)
	filter := domain.SavedStarGiftFilter{Owner: peer, BotAPI: true, ExcludeUnlimited: true, Limit: 1}
	page, err := gifts.ListByOwnerFiltered(ctx, filter)
	if err != nil || page.Count != 2 || len(page.Gifts) != 1 || page.Gifts[0].ID != second.ID || page.NextOffset == "" {
		t.Fatalf("filtered first page: %+v %v", page, err)
	}
	filter.Offset = page.NextOffset
	page, err = gifts.ListByOwnerFiltered(ctx, filter)
	if err != nil || page.Count != 2 || len(page.Gifts) != 1 || page.Gifts[0].ID != first.ID || page.NextOffset != "" {
		t.Fatalf("filtered last page: %+v %v", page, err)
	}
	filter.Offset, filter.ExcludeLimitedNonUpgradable = "", true
	page, err = gifts.ListByOwnerFiltered(ctx, filter)
	if err != nil || page.Count != 0 || len(page.Gifts) != 0 || page.NextOffset != "" {
		t.Fatalf("category exclusions must precede count: %+v %v", page, err)
	}
}
