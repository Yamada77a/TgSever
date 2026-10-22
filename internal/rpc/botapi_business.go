package rpc

import (
	"context"
	"errors"

	"telesrv/internal/domain"
)

func (r *Router) businessConnection(ctx context.Context, botID int64, connectionID string) (domain.ConnectedBusinessBot, error) {
	bot, err := r.BotAPISelf(ctx, botID)
	if err != nil {
		return domain.ConnectedBusinessBot{}, err
	}
	if bot.Deleted {
		return domain.ConnectedBusinessBot{}, errors.New("BOT_INVALID")
	}
	svc, ok := r.accountBusinessAutomation()
	if !ok {
		return domain.ConnectedBusinessBot{}, errors.New("BUSINESS_CONNECTION_INVALID")
	}
	connection, found, err := svc.GetBusinessBotConnection(ctx, connectionID)
	if err != nil {
		return domain.ConnectedBusinessBot{}, err
	}
	if !found || connection.BotUserID != botID || connectionID == "" {
		return domain.ConnectedBusinessBot{}, errors.New("BUSINESS_CONNECTION_INVALID")
	}
	owner, found, err := r.deps.Users.ByID(ctx, botID, connection.OwnerUserID)
	if err != nil {
		return domain.ConnectedBusinessBot{}, err
	}
	if !found || owner.Deleted || owner.Bot {
		return domain.ConnectedBusinessBot{}, errors.New("BUSINESS_CONNECTION_INVALID")
	}
	return connection, nil
}

func (r *Router) botAPIBusinessOwner(ctx context.Context, botID int64, connectionID string, right string) (int64, error) {
	connection, err := r.businessConnection(ctx, botID, connectionID)
	if err != nil {
		return 0, err
	}
	if connection.Disabled {
		return 0, errors.New("BUSINESS_CONNECTION_DISABLED")
	}
	if !connection.Rights.Allows(right) {
		return 0, errors.New("BUSINESS_BOT_RIGHT_REQUIRED")
	}
	return connection.OwnerUserID, nil
}

func (r *Router) botAPIBusinessPeer(ctx context.Context, botID int64, connectionID string, right string, peer domain.Peer) (int64, error) {
	ownerID, err := r.botAPIBusinessOwner(ctx, botID, connectionID, right)
	if err != nil {
		return 0, err
	}
	if peer.Type != domain.PeerTypeUser || peer.ID <= 0 || peer.ID == ownerID || peer.ID == botID {
		return 0, errors.New("CHAT_ID_INVALID")
	}
	svc, _ := r.accountBusinessAutomation()
	connection, found, err := svc.GetConnectedBusinessBot(ctx, ownerID)
	if err != nil {
		return 0, err
	}
	if !found || connection.ConnectionID != connectionID || connection.Disabled {
		return 0, errors.New("BUSINESS_CONNECTION_DISABLED")
	}
	state, _, err := svc.GetConnectedBusinessBotPeerState(ctx, ownerID, peer.ID)
	if err != nil {
		return 0, err
	}
	if state.Disabled || state.Paused {
		return 0, errors.New("BUSINESS_PEER_DISABLED")
	}
	existing, contact := false, false
	if connection.Recipients.ExistingChats != connection.Recipients.NewChats {
		if r.deps.Messages == nil || connection.CreatedAtUnix <= 0 {
			return 0, errors.New("INTERNAL")
		}
		before, err := r.deps.Messages.Search(ctx, ownerID, domain.MessageFilter{HasPeer: true, Peer: peer, MaxDate: int(connection.CreatedAtUnix) - 1, Limit: 1})
		if err != nil {
			return 0, err
		}
		existing = len(before.Messages) > 0
	}
	if connection.Recipients.Contacts != connection.Recipients.NonContacts {
		if r.deps.Contacts == nil {
			return 0, errors.New("INTERNAL")
		}
		settings, err := r.deps.Contacts.GetPeerSettings(ctx, ownerID, peer)
		if err != nil {
			return 0, err
		}
		contact = !settings.AddContact
	}
	if !domain.BusinessBotRecipientsMatch(connection.Recipients, existing, contact, peer.ID) {
		return 0, errors.New("BUSINESS_PEER_INVALID")
	}
	if right == "can_reply" || right == "can_read_messages" {
		if r.deps.Messages == nil {
			return 0, errors.New("INTERNAL")
		}
		senderID := int64(0)
		if right == "can_reply" {
			senderID = peer.ID
		}
		recent, err := r.deps.Messages.Search(ctx, ownerID, domain.MessageFilter{HasPeer: true, Peer: peer, SenderUserID: senderID, MinDate: int(r.clock.Now().Unix()) - 24*60*60, Limit: 1})
		if err != nil {
			return 0, err
		}
		if len(recent.Messages) == 0 {
			return 0, errors.New("BUSINESS_CHAT_NOT_ACTIVE")
		}
	}
	return ownerID, nil
}
