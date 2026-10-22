package rpc

import (
	"context"
	"errors"

	"github.com/iamxvbaba/td/tg"
	"telesrv/internal/domain"
)

type BotAPILiveLocationEdit = struct {
	Stop                                          bool
	Latitude, Longitude, Accuracy                 float64
	Period, Heading, Radius                       int
	SetAccuracy, SetPeriod, SetHeading, SetRadius bool
}
type BotAPIChecklistSendOptions = struct {
	Silent, Protect bool
	EffectID        int64
	ReplyID         int
	ReplyParameters *domain.BotAPIReplyParameters
}

func (r *Router) BotAPIDeleteMessages(ctx context.Context, botID, chatID int64, ids []int) (bool, error) {
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return false, err
	}
	if len(ids) < 1 || len(ids) > 100 {
		return false, errors.New("MESSAGE_IDS_INVALID")
	}
	for _, id := range ids {
		if id <= 0 || id > domain.MaxMessageBoxID {
			return false, messageIDInvalidErr()
		}
	}
	_, peer, err := r.botAPIInputPeer(ctx, botID, chatID)
	if err != nil {
		return false, err
	}
	if peer.Type == domain.PeerTypeChannel {
		history, err := r.deps.Channels.GetMessages(ctx, botID, peer.ID, ids)
		if err != nil {
			return false, err
		}
		if history.Self.Status != domain.ChannelMemberActive {
			return false, errors.New("CHAT_ID_INVALID")
		}
		selected := make([]int, 0, len(history.Messages))
		for _, msg := range history.Messages {
			if botAPIDeletionAllowed(botAPIMessageFromChannel(botID, msg), int(r.clock.Now().Unix())) {
				selected = append(selected, msg.ID)
			}
		}
		if len(selected) == 0 {
			return true, nil
		}
		ids = selected
		res, err := r.deps.Channels.DeleteMessages(ctx, botID, domain.DeleteChannelMessagesRequest{UserID: botID, ChannelID: peer.ID, IDs: ids, SkipUnauthorized: true, Date: int(r.clock.Now().Unix()), Audit: domain.BotDeletionAudit(botID)})
		if err != nil {
			return false, channelDeleteErr(err)
		}
		r.NotifyModerationChannelDeletion(ctx, res)
		return true, nil
	}
	list, err := r.deps.Messages.GetMessages(ctx, botID, ids)
	if err != nil {
		return false, err
	}
	selected := make([]int, 0, len(list.Messages))
	for _, msg := range list.Messages {
		if msg.Peer == peer && botAPIDeletionAllowed(msg, int(r.clock.Now().Unix())) {
			selected = append(selected, msg.ID)
		}
	}
	if len(selected) == 0 {
		return true, nil
	}
	res, err := r.deps.Messages.DeleteMessages(ctx, botID, domain.DeleteMessagesRequest{OwnerUserID: botID, IDs: selected, Revoke: true, Date: int(r.clock.Now().Unix()), Audit: domain.BotDeletionAudit(botID)})
	if err != nil {
		return false, err
	}
	r.notifyPushDeletes(res)
	r.enqueueBotAPIBusinessPrivateDeletes(ctx, res)
	return true, nil
}
func botAPILiveMedia(input BotAPILiveLocationEdit) *tg.InputMediaGeoLive {
	media := &tg.InputMediaGeoLive{Stopped: input.Stop, GeoPoint: &tg.InputGeoPointEmpty{}}
	if !input.Stop {
		geo := &tg.InputGeoPoint{Lat: input.Latitude, Long: input.Longitude}
		if input.SetAccuracy {
			geo.SetAccuracyRadius(int(input.Accuracy + 0.5))
		}
		media.GeoPoint = geo
		if input.SetPeriod {
			media.SetPeriod(input.Period)
		}
		if input.SetHeading {
			media.SetHeading(input.Heading)
		}
		if input.SetRadius {
			media.SetProximityNotificationRadius(input.Radius)
		}
	}
	return media
}
func (r *Router) BotAPIEditLiveLocation(ctx context.Context, botID, chatID int64, id int, connection string, input BotAPILiveLocationEdit, set bool, markup *domain.MessageReplyMarkup) (domain.Message, error) {
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return domain.Message{}, err
	}
	if connection != "" {
		peer, ok := botAPIPeerFromChatID(chatID)
		if !ok || peer.Type != domain.PeerTypeUser {
			return domain.Message{}, errors.New("CHAT_ID_INVALID")
		}
		owner, err := r.botAPIBusinessPeer(ctx, botID, connection, "can_reply", peer)
		if err != nil {
			return domain.Message{}, err
		}
		current, err := r.botAPIBusinessEditTarget(ctx, botID, owner, connection, peer, id)
		if err != nil {
			return domain.Message{}, err
		}
		media, err := editLiveLocationMedia(current.Media, current.Date, int(r.clock.Now().Unix()), botAPILiveMedia(input))
		if err != nil {
			return domain.Message{}, err
		}
		return r.botAPIBusinessEditMedia(ctx, botID, owner, connection, current, media, false, set, markup)
	}
	req := &tg.MessagesEditMessageRequest{ID: id}
	req.SetMedia(botAPILiveMedia(input))
	if wire := botAPIEditReplyMarkup(set, markup); wire != nil {
		req.SetReplyMarkup(wire)
	}
	return r.botAPIEditViaHandler(ctx, botID, chatID, req)
}
func (r *Router) BotAPIEditInlineLiveLocation(ctx context.Context, botID int64, id domain.BotInlineMessageID, input BotAPILiveLocationEdit, set bool, markup *domain.MessageReplyMarkup) (bool, error) {
	if _, err := r.BotAPISelf(ctx, botID); err != nil {
		return false, err
	}
	req := &tg.MessagesEditInlineBotMessageRequest{ID: tgInputBotInlineMessageID(id)}
	req.SetMedia(botAPILiveMedia(input))
	if wire := botAPIEditReplyMarkup(set, markup); wire != nil {
		req.SetReplyMarkup(wire)
	}
	return r.onMessagesEditInlineBotMessage(WithUserID(ctx, botID), req)
}
func botAPITodoMedia(todo domain.MessageTodo) *tg.InputMediaTodo {
	return &tg.InputMediaTodo{Todo: tg.TodoList{Title: tgTextWithEntities(todo.Title, todo.TitleEntities), List: tgTodoItems(todo.Items), OthersCanAppend: todo.OthersCanAppend, OthersCanComplete: todo.OthersCanComplete}}
}
func (r *Router) BotAPISendChecklist(ctx context.Context, botID, chatID int64, connection string, todo domain.MessageTodo, options BotAPIChecklistSendOptions, markup *domain.MessageReplyMarkup) (domain.Message, error) {
	if connection == "" {
		return domain.Message{}, errors.New("BUSINESS_CONNECTION_INVALID")
	}
	validated, err := domainTodoFromInput(botAPITodoMedia(todo).Todo)
	if err != nil {
		return domain.Message{}, err
	}
	return r.botAPISendContent(ctx, botID, chatID, "", nil, &domain.MessageMedia{Kind: domain.MessageMediaKindTodo, Todo: validated}, nil, markup, options.Silent, options.Protect, options.ReplyID, options.EffectID, domain.BotAPIMessageOptions{BusinessConnectionID: connection, ReplyParameters: options.ReplyParameters})
}
func (r *Router) BotAPIEditChecklist(ctx context.Context, botID, chatID int64, id int, connection string, todo domain.MessageTodo, set bool, markup *domain.MessageReplyMarkup) (domain.Message, error) {
	peer, ok := botAPIPeerFromChatID(chatID)
	if !ok || peer.Type != domain.PeerTypeUser {
		return domain.Message{}, errors.New("CHAT_ID_INVALID")
	}
	owner, err := r.botAPIBusinessPeer(ctx, botID, connection, "can_reply", peer)
	if err != nil {
		return domain.Message{}, err
	}
	current, err := r.botAPIBusinessEditTarget(ctx, botID, owner, connection, peer, id)
	if err != nil {
		return domain.Message{}, err
	}
	if current.Media == nil || current.Media.Todo == nil {
		return domain.Message{}, messageIDInvalidErr()
	}
	validated, err := domainTodoFromInput(botAPITodoMedia(todo).Todo)
	if err != nil {
		return domain.Message{}, err
	}
	return r.botAPIBusinessEditMedia(ctx, botID, owner, connection, current, &domain.MessageMedia{Kind: domain.MessageMediaKindTodo, Todo: validated}, true, set, markup)
}

func botAPIDeletionAllowed(msg domain.Message, now int) bool {
	if msg.Deleted || msg.Date <= 0 || int64(now)-int64(msg.Date) >= 48*3600 {
		return false
	}
	if msg.Action != nil && (msg.Action.Type == domain.ChannelActionCreate || msg.Action.Type == domain.ChannelActionTopicCreate) {
		return false
	}
	if msg.Media != nil && msg.Media.ServiceAction != nil && msg.Media.ServiceAction.Kind == domain.MessageServiceActionTopicCreate {
		return false
	}
	if msg.Peer.Type == domain.PeerTypeUser && msg.Media != nil && msg.Media.Kind == domain.MessageMediaKindDice && int64(now)-int64(msg.Date) <= 24*3600 {
		return false
	}
	return true
}
