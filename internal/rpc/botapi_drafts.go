package rpc

import (
	"context"
	"errors"

	"github.com/iamxvbaba/td/tg"
	"telesrv/internal/domain"
)

type BotAPIMessageDraft = struct {
	ID                  int64
	ThreadID            int
	Text                string
	Entities            []domain.MessageEntity
	Rich                *domain.BotAPIRichMessageInput
	CanStop, KeepOnStop bool
}

func (r *Router) BotAPISendDraft(ctx context.Context, botID, chatID int64, draft BotAPIMessageDraft) (bool, error) {
	if chatID <= 0 {
		return false, errors.New("CHAT_ID_INVALID")
	}
	if draft.ID == 0 {
		return false, errors.New("DRAFT_ID_INVALID")
	}
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return false, err
	}
	if draft.ThreadID != 0 {
		if err := r.botAPIPrivateTopic(ctx, botID, chatID, draft.ThreadID); err != nil {
			return false, err
		}
	}
	input, _, err := r.botAPIInputPeer(ctx, botID, chatID)
	if err != nil {
		return false, err
	}
	var action tg.SendMessageActionClass = &tg.SendMessageTextDraftAction{RandomID: draft.ID, Text: tgTextWithEntities(draft.Text, draft.Entities), CanStop: draft.CanStop, KeepOnStop: draft.KeepOnStop}
	if draft.Rich != nil {
		rich, err := tgInputRichMessageFromBotAPI(*draft.Rich)
		if err != nil {
			return false, err
		}
		action = &tg.InputSendMessageRichMessageDraftAction{RandomID: draft.ID, RichMessage: rich, CanStop: draft.CanStop, KeepOnStop: draft.KeepOnStop}
	}
	req := &tg.MessagesSetTypingRequest{Peer: input, Action: action}
	if draft.ThreadID != 0 {
		req.SetTopMsgID(draft.ThreadID)
	}
	return r.onMessagesSetTyping(WithUserID(ctx, botID), req)
}
