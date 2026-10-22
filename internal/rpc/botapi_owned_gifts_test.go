package rpc

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	appaccount "telesrv/internal/app/account"
	appgifts "telesrv/internal/app/stargifts"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/botapi"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// Exercise the query/projection library directly: no future HTTP registration
// or business mutation gateway is needed to consume an inventory page.
func TestBotAPIOwnedGiftQueryPrivacyAndPagination(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	owner, err := users.Create(ctx, domain.User{Phone: "15550009501", FirstName: "Owner", AccessHash: 1})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := users.Create(ctx, domain.User{Phone: "15550009502", FirstName: "Sender", AccessHash: 2})
	if err != nil {
		t.Fatal(err)
	}
	bot, _, err := memory.NewBotStore(users).CreateBotAccount(ctx,
		domain.User{FirstName: "Inventory", Username: "inventory_bot", AccessHash: 3},
		domain.BotProfile{OwnerUserID: owner.ID, TokenSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	gifts := memory.NewStarGiftStore()
	unlimited := domain.StarGift{ID: 1, RevisionID: 11, Stars: 50}
	limited := domain.StarGift{ID: 2, RevisionID: 12, Stars: 150, Limited: true,
		UpgradeStars: 100, UpgradeTotal: 10, Sticker: domain.Document{ID: 700, AccessHash: 7, DCID: 2, MimeType: "application/x-tgsticker"}}
	nonUpgrade := domain.StarGift{ID: 3, RevisionID: 13, Stars: 75, Limited: true}
	gifts.SeedCatalog([]domain.StarGift{unlimited, limited, nonUpgrade})
	if _, err := gifts.PublishCollectibleRevision(ctx, domain.StarGiftCollectibleWrite{
		GiftID: limited.ID, UpgradeStars: 100, SupplyTotal: 10, SlugPrefix: "inventory",
		Models:    []domain.StarGiftCollectibleAttribute{collectibleRPCAttribute(domain.StarGiftCollectibleModel, 7101, "Aurora"), collectibleRPCAttribute(domain.StarGiftCollectibleModel, 7102, "Day")},
		Patterns:  []domain.StarGiftCollectibleAttribute{collectibleRPCAttribute(domain.StarGiftCollectiblePattern, 7103, "Orbit"), collectibleRPCAttribute(domain.StarGiftCollectiblePattern, 7104, "Star")},
		Backdrops: []domain.StarGiftCollectibleAttribute{collectibleRPCAttribute(domain.StarGiftCollectibleBackdrop, 1, "Night"), collectibleRPCAttribute(domain.StarGiftCollectibleBackdrop, 2, "Light")},
		Actor:     "test", CommandID: "inventory-pool",
	}); err != nil {
		t.Fatal(err)
	}
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	receive := func(gift domain.StarGift, date int, hidden, unsaved bool, uniqueID int64) int64 {
		t.Helper()
		id, err := gifts.Create(ctx, domain.SavedStarGift{Owner: peer, FromUserID: sender.ID, GiftID: gift.ID, RevisionID: gift.RevisionID,
			MsgID: date + 100, Date: date, NameHidden: hidden, Unsaved: unsaved, UniqueGiftID: uniqueID,
			Message: "secret", MessageEntities: []domain.MessageEntity{{Type: domain.MessageEntityMentionName, Offset: 0, Length: 6, UserID: sender.ID}}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := receive(unlimited, 10, false, false, 0)
	receive(limited, 20, true, false, 0)
	receive(nonUpgrade, 30, false, false, 0)
	receive(limited, 40, false, true, 0)
	if err := gifts.SetPinned(ctx, peer, []int64{first}); err != nil {
		t.Fatal(err)
	}
	// New catalog appearance must not rewrite the received gift's snapshot or
	// make its historical limited category disappear.
	current := limited
	current.RevisionID, current.Stars, current.Limited, current.Sticker.ID = 22, 999, false, 999
	gifts.SeedCatalog([]domain.StarGift{current})
	passwords := memory.NewPasswordStore()
	account := appaccount.NewService(passwords, appaccount.WithBusinessAutomation(passwords))
	if _, err := account.SaveConnectedBusinessBot(ctx, owner.ID, domain.ConnectedBusinessBot{BotUserID: bot.ID,
		Recipients: domain.BusinessBotRecipients{ExistingChats: true, NewChats: true, Contacts: true, NonContacts: true},
		Rights:     domain.BusinessBotRights{ViewGifts: true}}); err != nil {
		t.Fatal(err)
	}
	r := New(Config{}, Deps{Users: appusers.NewService(users), Gifts: appgifts.NewService(gifts, nil, 2), Account: account},
		zaptest.NewLogger(t), fixedClock{now: time.Unix(100, 0)})
	page := func(values map[string]string) (domain.BotAPIOwnedGiftPage, map[string]any) {
		t.Helper()
		filter, ok := botapi.ParseOwnedGiftFilter(values)
		if !ok {
			t.Fatalf("invalid filter: %v", values)
		}
		got, err := r.BotAPIGetUserGifts(ctx, bot.ID, owner.ID, filter)
		if err != nil {
			t.Fatal(err)
		}
		return got, botapi.APIOwnedGifts(got)
	}
	p, _ := page(map[string]string{"limit": "1"})
	if p.Count != 3 || len(p.Gifts) != 1 || p.Gifts[0].Saved.Date != 30 || p.NextOffset == "" {
		t.Fatalf("first page: %+v", p)
	}
	p, projected := page(map[string]string{"limit": "1", "offset": p.NextOffset})
	private := projected["gifts"].([]map[string]any)[0]
	if p.Count != 3 || p.Gifts[0].Saved.Date != 20 || private["is_private"] != true || private["sender_user"] != nil || private["text"] != nil || private["entities"] != nil || private["owned_gift_id"] != nil {
		t.Fatalf("anonymous gift leaked: %+v", projected)
	}
	historical := private["gift"].(map[string]any)
	if historical["star_count"] != int64(150) || historical["upgrade_star_count"] != int64(100) || !p.Gifts[0].Gift.Limited || p.Gifts[0].Gift.Sticker.ID != 700 {
		t.Fatalf("historical projection: %+v", historical)
	}
	p, _ = page(map[string]string{"limit": "1", "offset": p.NextOffset})
	if p.Count != 3 || len(p.Gifts) != 1 || p.Gifts[0].Saved.Date != 10 || p.NextOffset != "" {
		t.Fatalf("last page: %+v", p)
	}
	for _, tc := range []struct {
		flag        string
		first, last int
	}{
		{"exclude_unlimited", 30, 20},
		{"exclude_limited_upgradable", 30, 10},
		{"exclude_limited_non_upgradable", 20, 10},
	} {
		values := map[string]string{"limit": "1", tc.flag: "true"}
		p, _ = page(values)
		if p.Count != 2 || len(p.Gifts) != 1 || p.Gifts[0].Saved.Date != tc.first || p.NextOffset == "" {
			t.Fatalf("%s first page: %+v", tc.flag, p)
		}
		values["offset"] = p.NextOffset
		p, _ = page(values)
		if p.Count != 2 || len(p.Gifts) != 1 || p.Gifts[0].Saved.Date != tc.last || p.NextOffset != "" {
			t.Fatalf("%s last page: %+v", tc.flag, p)
		}
	}
	p, _ = page(map[string]string{"limit": "1", "sort_by_price": "true"})
	if p.Gifts[0].Saved.Date != 20 || p.NextOffset == "" {
		t.Fatalf("price order: %+v", p)
	}
	p, _ = page(map[string]string{"limit": "1", "sort_by_price": "true", "offset": p.NextOffset})
	if p.Count != 3 || len(p.Gifts) != 1 || p.Gifts[0].Saved.Date != 30 || p.NextOffset == "" {
		t.Fatalf("price keyset: %+v", p)
	}
	business, err := r.botAPIOwnedGifts(ctx, bot.ID, peer, domain.SavedStarGiftFilter{Limit: 100}, true)
	if err != nil || business.Count != 4 {
		t.Fatalf("authorized business page: %+v %v", business, err)
	}
	private = botapi.APIOwnedGifts(business)["gifts"].([]map[string]any)[2]
	if private["text"] != "secret" || private["owned_gift_id"] != strconv.Itoa(120) || private["sender_user"].(map[string]any)["id"] != sender.ID {
		t.Fatalf("authorized private details: %+v", private)
	}
	if revoked, err := account.DeleteConnectedBusinessBot(ctx, owner.ID, bot.ID); err != nil || !revoked {
		t.Fatalf("revoke business connection: %v %v", revoked, err)
	}
	if _, err := r.botAPIOwnedGifts(ctx, bot.ID, peer, domain.SavedStarGiftFilter{Limit: 100}, true); err == nil {
		t.Fatal("disabled business connection exposed inventory")
	}
	unique := domain.UniqueStarGift{ID: 9, GiftID: limited.ID, Title: "Comet", Slug: "Comet-1", Num: 1, Owner: peer,
		OwnerAddress: "private-wallet", OriginalFromUserID: sender.ID, OriginalMessage: "original secret", Model: collectibleRPCAttribute(domain.StarGiftCollectibleModel, 7201, "Aurora")}
	gifts.SeedUnique(unique)
	receive(limited, 50, true, false, unique.ID)
	p, projected = page(map[string]string{"limit": "1"})
	public := projected["gifts"].([]map[string]any)[0]
	publicUnique := public["gift"].(map[string]any)
	if public["type"] != "unique" || public["sender_user"] != nil || public["owned_gift_id"] != nil || public["text"] != nil || publicUnique["name"] != "Comet-1" || publicUnique["is_from_blockchain"] != true || publicUnique["owner_address"] != nil || publicUnique["original_details"] != nil {
		t.Fatalf("public unique projection: %+v", public)
	}
	p, _ = page(map[string]string{"exclude_from_blockchain": "true"})
	if p.Count != 3 {
		t.Fatalf("blockchain filter count: %+v", p)
	}
}
