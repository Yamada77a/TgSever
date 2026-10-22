package rpc

import (
	"context"
	"encoding/json"
	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"sync"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
	"testing"
	"time"
)

type messageDraftSessions struct {
	captureSessions
	muDraft  sync.Mutex
	previews map[int64][]byte
}

func (s *messageDraftSessions) PushToUserTransientExceptAuthKeySession(_ context.Context, userID int64, _ [8]byte, _ int64, _ proto.MessageType, message tg.UpdatesClass, _ time.Duration) (int, error) {
	var buffer bin.Buffer
	if err := message.Encode(&buffer); err != nil {
		return 0, err
	}
	s.muDraft.Lock()
	defer s.muDraft.Unlock()
	if s.previews == nil {
		s.previews = make(map[int64][]byte)
	}
	s.previews[userID] = buffer.Buf
	return 1, nil
}
func (s *messageDraftSessions) action(t *testing.T, userID int64) tg.SendMessageActionClass {
	t.Helper()
	s.muDraft.Lock()
	raw := append([]byte(nil), s.previews[userID]...)
	s.muDraft.Unlock()
	buffer := bin.Buffer{Buf: raw}
	var update tg.UpdateShort
	if err := update.Decode(&buffer); err != nil {
		t.Fatal(err)
	}
	typing, ok := update.Update.(*tg.UpdateUserTyping)
	if !ok {
		t.Fatalf("preview update: %T", update.Update)
	}
	return typing.Action
}

func dispatchDraftStop(t *testing.T, r *Router, user domain.User, bot domain.User, id int64, thread int) error {
	t.Helper()
	request := &tg.MessagesSetTypingRequest{Peer: inputPeerUser(bot), Action: &tg.SendMessageStopDraftAction{RandomID: id}}
	if thread != 0 {
		request.SetTopMsgID(thread)
	}
	var wire bin.Buffer
	if err := request.Encode(&wire); err != nil {
		t.Fatal(err)
	}
	_, err := r.Dispatch(WithUserID(context.Background(), user.ID), [8]byte{1}, 77, &wire)
	return err
}

type nativeMessageDraft struct {
	ID                        int64
	ThreadID                  int
	Text                      string
	CanStop, KeepOnStop, Rich bool
}

func dispatchNativeDraft(t *testing.T, r *Router, bot, user domain.User, draft nativeMessageDraft) (bool, error) {
	t.Helper()
	var action tg.SendMessageActionClass = &tg.SendMessageTextDraftAction{
		RandomID: draft.ID, CanStop: draft.CanStop, KeepOnStop: draft.KeepOnStop,
		Text: tg.TextWithEntities{Text: draft.Text},
	}
	if draft.Rich {
		action = &tg.InputSendMessageRichMessageDraftAction{
			RandomID: draft.ID, CanStop: draft.CanStop, KeepOnStop: draft.KeepOnStop,
			RichMessage: &tg.InputRichMessageHTML{HTML: "<p>" + draft.Text + "</p>"},
		}
	}
	request := &tg.MessagesSetTypingRequest{Peer: inputPeerUser(user), Action: action}
	if draft.ThreadID != 0 {
		request.SetTopMsgID(draft.ThreadID)
	}
	var wire bin.Buffer
	if err := request.Encode(&wire); err != nil {
		t.Fatal(err)
	}
	_, err := r.Dispatch(WithUserID(context.Background(), bot.ID), [8]byte{1}, 77, &wire)
	return err == nil, err
}

func TestMessageDraftLifecycleNativeStopAndBotAPIEvent(t *testing.T) {
	for _, rich := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "rich"}[rich], func(t *testing.T) {
			fixture := newInlineBotRPCTestFixture(t)
			r := fixture.router
			r.deps.MessageDrafts = memory.NewMessageDraftStore()
			r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
			sessions := &messageDraftSessions{}
			r.deps.Sessions = sessions
			draft := nativeMessageDraft{ID: 1<<60 + 7, Text: "partial", CanStop: true, KeepOnStop: true}
			if rich {
				draft.Rich = true
			}
			if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
				t.Fatalf("send draft: %v %v", ok, err)
			}
			if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); err != nil {
				t.Fatalf("wire stop: %v", err)
			}
			stopped, ok := sessions.action(t, fixture.bot.ID).(*tg.SendMessageStopDraftAction)
			if !ok || stopped.RandomID != draft.ID {
				t.Fatalf("native bot stop: %#v", stopped)
			}
			switch kept := sessions.action(t, fixture.peer.ID).(type) {
			case *tg.SendMessageTextDraftAction:
				if kept.CanStop || kept.Text.Text != "partial" {
					t.Fatalf("kept text: %+v", kept)
				}
			case *tg.SendMessageRichMessageDraftAction:
				if kept.CanStop {
					t.Fatalf("kept rich still stoppable: %+v", kept)
				}
			default:
				t.Fatalf("kept preview: %T", kept)
			}
			if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); ok || !tgerr.Is(err, "MESSAGE_GENERATION_STOPPED") {
				t.Fatalf("update after stop: %v %v", ok, err)
			}
			if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); err != nil {
				t.Fatal(err)
			}
			events, err := r.BotAPIUpdates(context.Background(), fixture.bot.ID, 0)
			if err != nil || len(events) != 1 || events[0].BotAPIObject == nil {
				t.Fatalf("stopped events: %+v %v", events, err)
			}
			var payload struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
				DraftID int64 `json:"draft_id"`
			}
			if err := json.Unmarshal(events[0].BotAPIObject.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if events[0].BotAPIObject.Kind != "stopped_message_generation" || payload.Chat.ID != fixture.peer.ID || payload.DraftID != draft.ID {
				t.Fatalf("stopped event: %+v", payload)
			}
			// A non-kept stop removes the preview instead of persisting a partial message.
			draft.ID++
			draft.KeepOnStop = false
			if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
				t.Fatalf("replacement: %v %v", ok, err)
			}
			if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); err != nil {
				t.Fatal(err)
			}
			if _, ok := sessions.action(t, fixture.peer.ID).(*tg.SendMessageCancelAction); !ok {
				t.Fatal("non-kept preview not removed")
			}
			history, err := r.deps.Messages.GetHistory(context.Background(), fixture.peer.ID, domain.MessageFilter{HasPeer: true, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: fixture.bot.ID}, Limit: 10})
			if err != nil || len(history.Messages) != 0 {
				t.Fatalf("draft became ordinary history: %+v %v", history, err)
			}
		})
	}
}

func TestMessageDraftFinalSendTerminatesPreview(t *testing.T) {
	for _, media := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "media"}[media], func(t *testing.T) {
			fixture := newInlineBotRPCTestFixture(t)
			r := fixture.router
			r.deps.MessageDrafts = memory.NewMessageDraftStore()
			r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
			draft := nativeMessageDraft{ID: 21, Text: "partial", CanStop: true}
			if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
				t.Fatalf("draft: %v %v", ok, err)
			}
			ctx := WithUserID(context.Background(), fixture.bot.ID)
			var err error
			if media {
				_, err = r.onMessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
					Peer: inputPeerUser(fixture.peer), RandomID: 9001,
					Media: &tg.InputMediaContact{PhoneNumber: "15550007103", FirstName: "Contact"},
				})
			} else {
				_, err = r.onMessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: inputPeerUser(fixture.peer), RandomID: 9001, Message: "complete"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); ok || !tgerr.Is(err, "MESSAGE_GENERATION_STOPPED") {
				t.Fatalf("preview resurrected after final send: %v %v", ok, err)
			}
			if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); !tgerr.Is(err, "DRAFT_ID_INVALID") {
				t.Fatalf("stop after final send: %v", err)
			}
		})
	}
}

func TestMessageDraftStopAuthorizationAndExpiry(t *testing.T) {
	fixture := newInlineBotRPCTestFixture(t)
	r := fixture.router
	r.deps.MessageDrafts = memory.NewMessageDraftStore()
	r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
	draft := nativeMessageDraft{ID: 44, Text: "partial", CanStop: false}
	if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
		t.Fatalf("draft: %v %v", ok, err)
	}
	if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); !tgerr.Is(err, "DRAFT_STOP_FORBIDDEN") {
		t.Fatalf("unstoppable draft: %v", err)
	}
	draft.CanStop = true
	if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
		t.Fatalf("enable stop: %v %v", ok, err)
	}
	if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 9); !tgerr.Is(err, "TOPIC_ID_INVALID") {
		t.Fatalf("wrong thread stop: %v", err)
	}
	forged := fixture.bot
	forged.AccessHash++
	if err := dispatchDraftStop(t, r, fixture.peer, forged, draft.ID, 0); err == nil {
		t.Fatal("forged peer hash stopped another preview")
	}
	if err := dispatchDraftStop(t, r, fixture.bot, fixture.peer, draft.ID, 0); !tgerr.Is(err, "USER_BOT_INVALID") {
		t.Fatalf("bot requester stopped user preview: %v", err)
	}
	events, err := r.BotAPIUpdates(context.Background(), fixture.bot.ID, 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("rejected stop emitted event: %+v %v", events, err)
	}
	r.clock = fixedClock{now: r.clock.Now().Add(domain.MessageDraftLifetime)}
	if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, 0); !tgerr.Is(err, "DRAFT_ID_INVALID") {
		t.Fatalf("expired stop: %v", err)
	}
}

func TestMessageDraftPrivateTopicStopUsesCanonicalThread(t *testing.T) {
	fixture := newInlineBotRPCTestFixture(t)
	r := fixture.router
	r.deps.MessageDrafts = memory.NewMessageDraftStore()
	r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
	sessions := &messageDraftSessions{}
	r.deps.Sessions = sessions
	if _, err := r.deps.Messages.SendPrivateText(context.Background(), fixture.peer.ID, domain.SendPrivateTextRequest{SenderUserID: fixture.peer.ID, RecipientUserID: fixture.peer.ID, RandomID: 8001, Message: "saved message", Date: int(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	topic, err := r.botAPICreatePrivateForumTopic(context.Background(), fixture.bot.ID, fixture.peer.ID, "Generation", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	peerThreadID, err := r.privateForumPeerThread(context.Background(), fixture.bot.ID, fixture.peer.ID, topic.TopicID)
	if err != nil {
		t.Fatal(err)
	}
	if peerThreadID == topic.TopicID {
		t.Fatal("fixture must have distinct sender/recipient topic roots")
	}
	draft := nativeMessageDraft{ID: 51, ThreadID: topic.TopicID, Text: "partial", CanStop: true}
	if ok, err := dispatchNativeDraft(t, r, fixture.bot, fixture.peer, draft); err != nil || !ok {
		t.Fatalf("topic draft: %v %v", ok, err)
	}
	sessions.muDraft.Lock()
	raw := append([]byte(nil), sessions.previews[fixture.peer.ID]...)
	sessions.muDraft.Unlock()
	buffer := bin.Buffer{Buf: raw}
	var preview tg.UpdateShort
	if err := preview.Decode(&buffer); err != nil {
		t.Fatal(err)
	}
	if got := preview.Update.(*tg.UpdateUserTyping).TopMsgID; got != peerThreadID {
		t.Fatalf("native user thread = %d want %d", got, peerThreadID)
	}
	if err := dispatchDraftStop(t, r, fixture.peer, fixture.bot, draft.ID, peerThreadID); err != nil {
		t.Fatal(err)
	}
	events, err := r.BotAPIUpdates(context.Background(), fixture.bot.ID, 0)
	if err != nil || len(events) != 1 || events[0].BotAPIObject == nil {
		t.Fatalf("topic stop events: %+v %v", events, err)
	}
	var payload struct {
		ThreadID int `json:"message_thread_id"`
	}
	if err := json.Unmarshal(events[0].BotAPIObject.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ThreadID != topic.TopicID {
		t.Fatalf("thread id = %d want %d", payload.ThreadID, topic.TopicID)
	}
}

func TestMessageDraftStopHonorsLiveRestrictedBotAccess(t *testing.T) {
	f := newInlineBotRPCTestFixture(t)
	r := f.router
	r.deps.MessageDrafts = memory.NewMessageDraftStore()
	r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
	r.deps.Sessions = &messageDraftSessions{}
	ctx := context.Background()
	draft := nativeMessageDraft{ID: 91, Text: "partial", CanStop: true}
	if ok, err := dispatchNativeDraft(t, r, f.bot, f.peer, draft); err != nil || !ok {
		t.Fatalf("initial draft: %v %v", ok, err)
	}
	if err := f.bots.SetBotAccessSettings(ctx, f.owner.ID, f.bot.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.onMessagesSetTyping(WithUserID(ctx, f.peer.ID), &tg.MessagesSetTypingRequest{Peer: inputPeerUser(f.bot), Action: &tg.SendMessageTypingAction{}}); ok || !tgerr.Is(err, "BOT_ACCESS_DENIED") {
		t.Fatalf("restricted typing: %v %v", ok, err)
	}
	if err := dispatchDraftStop(t, r, f.peer, f.bot, draft.ID, 0); !tgerr.Is(err, "BOT_ACCESS_DENIED") {
		t.Fatalf("restricted stop: %v", err)
	}
	events, err := r.BotAPIUpdates(ctx, f.bot.ID, 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("denied stop produced event: %+v %v", events, err)
	}
	if err := f.bots.SetBotAccessSettings(ctx, f.owner.ID, f.bot.ID, true, []int64{f.peer.ID}); err != nil {
		t.Fatal(err)
	}
	if err := dispatchDraftStop(t, r, f.peer, f.bot, draft.ID, 0); err != nil {
		t.Fatalf("stop after live allowlist change: %v", err)
	}
	events, err = r.BotAPIUpdates(ctx, f.bot.ID, 0)
	if err != nil || len(events) != 1 || events[0].BotAPIObject == nil || events[0].BotAPIObject.Kind != "stopped_message_generation" {
		t.Fatalf("allowed stop event: %+v %v", events, err)
	}
}

func TestMessageDraftBlockedStopPreservesGeneration(t *testing.T) {
	r, bot, user, _ := typingDraftRouter(t)
	r.deps.BotAPIUpdates = memory.NewBotAPIUpdateStore()
	draft := nativeMessageDraft{ID: 101, Text: "partial", CanStop: true}
	forged := user
	forged.AccessHash++
	if ok, err := dispatchNativeDraft(t, r, bot, forged, draft); ok || err == nil {
		t.Fatalf("forged recipient accepted: %v %v", ok, err)
	}
	if ok, err := dispatchNativeDraft(t, r, bot, user, draft); err != nil || !ok {
		t.Fatalf("draft: %v %v", ok, err)
	}
	ctx := WithUserID(context.Background(), user.ID)
	if ok, err := r.onContactsBlock(ctx, &tg.ContactsBlockRequest{ID: inputPeerUser(bot)}); err != nil || !ok {
		t.Fatalf("block: %v %v", ok, err)
	}
	if err := dispatchDraftStop(t, r, user, bot, draft.ID, 0); !tgerr.Is(err, "USER_IS_BLOCKED") {
		t.Fatalf("blocked stop: %v", err)
	}
	events, err := r.BotAPIUpdates(ctx, bot.ID, 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("blocked stop emitted event: %+v %v", events, err)
	}
	if ok, err := r.onContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: inputPeerUser(bot)}); err != nil || !ok {
		t.Fatalf("unblock: %v %v", ok, err)
	}
	if err := dispatchDraftStop(t, r, user, bot, draft.ID, 0); err != nil {
		t.Fatalf("generation lost after rejected stop: %v", err)
	}
	events, err = r.BotAPIUpdates(ctx, bot.ID, 0)
	if err != nil || len(events) != 1 || events[0].BotAPIObject == nil || events[0].BotAPIObject.Kind != "stopped_message_generation" {
		t.Fatalf("accepted stop event: %+v %v", events, err)
	}
}
