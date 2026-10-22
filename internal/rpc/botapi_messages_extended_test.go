package rpc

import (
	"context"
	"fmt"
	"testing"

	"telesrv/internal/domain"
)

func TestBotAPIOrdinaryMediaPersistsBothMessageKinds(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	for _, chatID := range []int64{f.member.ID, f.chatID} {
		for _, media := range []*domain.MessageMedia{
			{Kind: domain.MessageMediaKindContact, Contact: &domain.MessageContact{PhoneNumber: "15551234567", FirstName: "Ada"}},
			{Kind: domain.MessageMediaKindGeo, Geo: &domain.MessageGeoPoint{Lat: 10, Long: 20}},
			{Kind: domain.MessageMediaKindVenue, Venue: &domain.MessageVenue{Geo: domain.MessageGeoPoint{Lat: 10, Long: 20}, Title: "Station", Address: "Road"}},
		} {
			sent, err := f.router.BotAPISendOrdinary(f.ctx, domain.BotAPIEphemeralSendInput{BotUserID: f.bot.ID, ChatID: chatID, Kind: "direct", DirectMedia: media}, domain.BotAPIMessageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := f.router.botAPIReloadMessage(f.ctx, f.bot.ID, sent.Peer, sent.ID)
			if err != nil || loaded.Media == nil || loaded.Media.Kind != media.Kind {
				t.Fatalf("ordinary media lost=%+v %v", loaded, err)
			}
		}
	}
}

func (f *fakeFiles) CreateDocumentFromURLWithSpec(ctx context.Context, rawURL string, spec domain.DocumentSpec) (domain.Document, error) {
	doc, err := f.CreateDocumentFromURL(ctx, rawURL)
	if err != nil {
		return doc, err
	}
	doc.Attributes = spec.Attributes
	if spec.MimeType != "" {
		doc.MimeType = spec.MimeType
	}
	if f.docs == nil {
		f.docs = make(map[int64]domain.Document)
	}
	f.docs[doc.ID] = doc
	return doc, nil
}

func TestBotAPIMediaKindsPersistReusableAttributes(t *testing.T) {
	f := newBotAPIBridgeFixture(t)
	for _, kind := range []string{"animation", "audio", "video", "voice", "sticker", "video_note"} {
		file := domain.BotAPIFileInput{Bytes: []byte("media bytes"), FileName: "asset", MimeType: "application/octet-stream", Width: 320, Height: 320, Duration: 4, Title: "Title", Performer: "Performer", Emoji: "🙂", SupportsStreaming: true}
		sent, err := f.router.BotAPISendMediaFiles(f.ctx, f.bot.ID, f.member.ID, kind, file, domain.BotAPIFileInput{}, "caption", nil, nil, false, 0)
		if err != nil {
			t.Fatalf("%s=%v", kind, err)
		}
		if sent.Media == nil || sent.Media.Document == nil || !botAPIDocumentMatchesKind(*sent.Media.Document, kind) {
			t.Fatalf("%s attrs=%+v", kind, sent.Media)
		}
		reused, err := f.router.BotAPISendMediaFiles(f.ctx, f.bot.ID, f.chatID, kind, domain.BotAPIFileInput{LocationKey: fmt.Sprintf("doc:%d", sent.Media.Document.ID)}, domain.BotAPIFileInput{}, "reused", nil, nil, false, 0)
		if err != nil || reused.Media == nil || reused.Media.Document == nil || reused.Media.Document.ID != sent.Media.Document.ID {
			t.Fatalf("%s reused=%+v %v", kind, reused, err)
		}
	}
}
