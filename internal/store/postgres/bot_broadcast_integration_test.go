package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/storetest"
)

func TestBotBroadcastPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	bots := NewBotStore(pool)
	messages := NewMessageStore(pool)
	stars := NewStarsStore(pool)
	owner := createTestUser(t, ctx, users, "+1898"+suffix+"01", "Broadcast owner", "")
	recipient := createTestUser(t, ctx, users, "+1898"+suffix+"02", "Broadcast recipient", "")
	bot, _, err := bots.CreateBotAccount(ctx, domain.User{AccessHash: 9981, FirstName: "Broadcast bot", Username: "broadcast_" + suffix + "_bot"}, domain.BotProfile{OwnerUserID: owner.ID, TokenSecret: strings.Repeat("b", domain.BotTokenSecretLength)})
	if err != nil {
		t.Fatal(err)
	}
	commerce := NewBotCommerceStore(pool)
	storetest.BotBroadcast(t, storetest.BotBroadcastFixture{Ledger: commerce, Reopen: func() store.BotBroadcastStore { return NewBotCommerceStore(pool) }, Messages: messages, Commerce: commerce, BotID: bot.ID, OwnerID: owner.ID, RecipientID: recipient.ID, Fund: func(amount int64) error {
		date := int(time.Now().Unix())
		if _, err := stars.Credit(ctx, owner.ID, amount, domain.StarsReasonTopup, domain.Peer{}, date, "fund broadcasting", ""); err != nil {
			return err
		}
		for amount > 0 {
			part := min(amount, 10_000)
			if err := commerce.BusinessTransfer(ctx, owner.ID, bot.ID, part, date); err != nil {
				return err
			}
			amount -= part
		}
		return nil
	}})
}

func TestBotStarsLegacyMutationsPreserveBroadcastNanos(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	now := time.Now().UTC()
	date := int(now.Unix())
	users := NewUserStore(pool)
	owner := createTestUser(t, ctx, users, "+1899"+suffix+"01", "Fractional wallet owner", "")
	bot, _, err := NewBotStore(pool).CreateBotAccount(ctx, domain.User{AccessHash: 9982, FirstName: "Fractional wallet bot", Username: "fractional_" + suffix + "_bot"}, domain.BotProfile{OwnerUserID: owner.ID, TokenSecret: strings.Repeat("n", domain.BotTokenSecretLength)})
	if err != nil {
		t.Fatal(err)
	}
	messages := NewMessageStore(pool)
	commerce := NewBotCommerceStore(pool)
	lifecycle := NewStarGiftLifecycleStore(pool, messages, 0)
	credit := func(key string, amount int64) {
		t.Helper()
		reason := domain.StarsReasonBotInvoice
		if amount < 0 {
			reason = domain.StarsReasonBotRefund
		}
		if _, _, err := lifecycle.CreditBotStarsWallet(ctx, domain.BotStarsCredit{BotUserID: bot.ID, PayerUserID: owner.ID, Amount: amount, Reason: reason, InvoiceKey: key + suffix, Date: date}); err != nil {
			t.Fatal(err)
		}
	}
	balance := func(want domain.StarsAmount) {
		t.Helper()
		got, err := commerce.BotStarsBalanceExact(ctx, bot.ID)
		if err != nil || got != want {
			t.Fatalf("wallet=%+v err=%v want=%+v", got, err, want)
		}
	}
	credit("initial-", 11_000)
	if err := commerce.SetBotPaidBroadcast(ctx, owner.ID, bot.ID, true, date); err != nil {
		t.Fatal(err)
	}
	for i := range domain.BotBroadcastFreeLimit {
		if _, err := commerce.ReserveBotBroadcast(ctx, domain.BotBroadcastRequest{BotID: bot.ID, Key: fmt.Sprintf("free-%d", i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	reservation, err := commerce.ReserveBotBroadcast(ctx, domain.BotBroadcastRequest{BotID: bot.ID, Key: "fractional-send", AllowPaid: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.SendPrivateText(store.WithBotBroadcast(ctx, commerce, reservation.Request, func() time.Time { return now }), domain.SendPrivateTextRequest{SenderUserID: bot.ID, RecipientUserID: owner.ID, RandomID: 1, Message: "A real fractional broadcast", Date: date}); err != nil {
		t.Fatal(err)
	}
	balance(domain.StarsAmount{Amount: 10_999, Nanos: 900_000_000})
	credit("invoice-", 5)
	balance(domain.StarsAmount{Amount: 11_004, Nanos: 900_000_000})
	credit("refund-", -3)
	balance(domain.StarsAmount{Amount: 11_001, Nanos: 900_000_000})
	if err := withTx(ctx, pool, "spend bot wallet with fractional remainder", func(tx pgx.Tx) error {
		_, err := debitBotStarsWallet(ctx, tx, bot.ID, 2, domain.StarsReasonPaidMedia, domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}, date)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	balance(domain.StarsAmount{Amount: 10_999, Nanos: 900_000_000})
	page, err := lifecycle.BotStarsTransactions(ctx, bot.ID, domain.StarsTransactionQuery{Limit: 100})
	if err != nil || page.Balance != 10_999 || page.BalanceNanos != 900_000_000 {
		t.Fatalf("legacy wallet page=%+v err=%v", page, err)
	}
	for _, row := range page.Transactions {
		if row.Reason == domain.StarsTransactionReason("paid_broadcast") {
			if row.Amount != 0 || row.Nanos != -100_000_000 {
				t.Fatalf("fractional fee=%+v", row)
			}
			return
		}
	}
	t.Fatal("legacy wallet history omitted the fractional broadcast fee")
}
