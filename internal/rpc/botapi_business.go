package rpc

import (
	"context"
	"errors"

	"telesrv/internal/botapi"
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

func (r *Router) botAPIEnqueueBusinessMessage(ctx context.Context, botID int64, connectionID string, msg domain.Message, edited bool) {
	if msg.ID <= 0 || msg.Deleted || r.deps.Users == nil {
		return
	}
	var users []domain.User
	for _, id := range []int64{msg.OwnerUserID, msg.From.ID, msg.Peer.ID} {
		if id <= 0 {
			continue
		}
		if user, found, err := r.deps.Users.ByID(ctx, botID, id); err == nil && found {
			users = append(users, user)
		}
	}
	object := botapi.SnapshotMessage(msg, users, nil)
	object["business_connection_id"] = connectionID
	if msg.BusinessBotID != 0 {
		if sender, found, err := r.deps.Users.ByID(ctx, botID, msg.BusinessBotID); err == nil && found {
			object["sender_business_bot"] = map[string]any{"id": sender.ID, "is_bot": true, "first_name": sender.FirstName, "username": sender.Username}
		}
	}
	kind := domain.BotAPIUpdateKind("business_message")
	if edited {
		kind = domain.BotAPIUpdateKind("edited_business_message")
	}
	r.enqueueBotAPIObject(ctx, botID, kind, object)
}

func (r *Router) enqueueBotAPIBusinessPrivateMessage(ctx context.Context, result domain.SendPrivateTextResult) {
	if result.Duplicate {
		return
	}
	for _, message := range []domain.Message{result.SenderMessage, result.RecipientMessage} {
		r.enqueueConnectedBusinessMessage(ctx, message, false)
	}
}

func (r *Router) enqueueBotAPIBusinessPrivateEdits(ctx context.Context, result domain.EditMessageResult) {
	for _, item := range result.Edited {
		r.enqueueConnectedBusinessMessage(ctx, item.Message, true)
	}
}

func (r *Router) enqueueConnectedBusinessMessage(ctx context.Context, message domain.Message, edited bool) {
	svc, ok := r.accountBusinessAutomation()
	if !ok || message.ID == 0 || message.Peer.Type != domain.PeerTypeUser || message.Deleted {
		return
	}
	connection, found, err := svc.GetConnectedBusinessBot(ctx, message.OwnerUserID)
	if err != nil || !found || connection.Disabled {
		return
	}
	if connection.ConnectionID == message.BusinessConnectionID {
		return
	}
	if _, err := r.botAPIBusinessPeer(ctx, connection.BotUserID, connection.ConnectionID, "", message.Peer); err != nil {
		return
	}
	r.botAPIEnqueueBusinessMessage(ctx, connection.BotUserID, connection.ConnectionID, message, edited)
}
