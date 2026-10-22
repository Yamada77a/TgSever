package rpc

import (
	"context"
	"errors"

	"github.com/iamxvbaba/td/tg"
	"telesrv/internal/domain"
)

func (r *Router) BotAPISetMessageReaction(ctx context.Context, botID, chatID int64, id int, reactions []domain.MessageReaction, big bool) (bool, error) {
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return false, err
	}
	if len(reactions) > 1 {
		return false, errors.New("REACTIONS_TOO_MANY")
	}
	input, _, err := r.botAPIInputPeer(ctx, botID, chatID)
	if err != nil {
		return false, err
	}
	wire := make([]tg.ReactionClass, 0, len(reactions))
	for _, reaction := range reactions {
		if reaction.Type == domain.MessageReactionEmoji {
			if err := r.validateDefaultReaction(ctx, reaction); err != nil {
				return false, err
			}
		}
	}
	for _, reaction := range reactions {
		switch reaction.Type {
		case domain.MessageReactionEmoji:
			wire = append(wire, &tg.ReactionEmoji{Emoticon: reaction.Emoticon})
		case domain.MessageReactionCustomEmoji:
			if reaction.DocumentID <= 0 {
				return false, reactionInvalidErr()
			}
			wire = append(wire, &tg.ReactionCustomEmoji{DocumentID: reaction.DocumentID})
		default:
			return false, reactionInvalidErr()
		}
	}
	req := &tg.MessagesSendReactionRequest{Peer: input, MsgID: id, Big: big}
	req.SetReaction(wire)
	_, err = r.onMessagesSendReaction(WithUserID(ctx, botID), req)
	return err == nil, err
}

func (r *Router) BotAPIDeleteMessageReaction(ctx context.Context, botID, chatID int64, id int, actor domain.Peer) (bool, error) {
	peer, err := r.botAPIReactionModerationPeer(ctx, botID, chatID, actor)
	if err != nil {
		return false, err
	}
	_, err = r.deleteParticipantReaction(ctx, botID, peer, id, actor)
	return err == nil, err
}

func (r *Router) BotAPIDeleteAllMessageReactions(ctx context.Context, botID, chatID int64, actor domain.Peer) (bool, error) {
	peer, err := r.botAPIReactionModerationPeer(ctx, botID, chatID, actor)
	if err != nil {
		return false, err
	}
	return r.deleteParticipantReactions(ctx, botID, peer, actor)
}

func (r *Router) botAPIReactionModerationPeer(ctx context.Context, botID, chatID int64, actor domain.Peer) (domain.Peer, error) {
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return domain.Peer{}, err
	}
	_, peer, err := r.botAPIInputPeer(ctx, botID, chatID)
	if err != nil {
		return domain.Peer{}, err
	}
	if peer.Type != domain.PeerTypeChannel || r.deps.Channels == nil {
		return domain.Peer{}, errors.New("CHAT_ID_INVALID")
	}
	history, err := r.deps.Channels.GetMessages(ctx, botID, peer.ID, nil)
	if err != nil {
		return domain.Peer{}, err
	}
	if history.Channel.Broadcast {
		return domain.Peer{}, errors.New("CHAT_ID_INVALID")
	}
	if !domain.ReactionActorValid(actor) {
		return domain.Peer{}, errors.New("PARTICIPANT_ID_INVALID")
	}
	return peer, nil
}
