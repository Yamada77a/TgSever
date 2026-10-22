package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"telesrv/internal/domain"
)

type MessageExtendedGateway interface {
	BotAPISetMessageReaction(context.Context, int64, int64, int, []domain.MessageReaction, bool) (bool, error)
	BotAPIDeleteMessageReaction(context.Context, int64, int64, int, domain.Peer) (bool, error)
	BotAPIDeleteAllMessageReactions(context.Context, int64, int64, domain.Peer) (bool, error)
}

func init() {
	registerMethods(map[string]methodFunc{
		"setMessageReaction":        (*handler).setMessageReaction,
		"deleteMessageReaction":     (*handler).deleteMessageReaction,
		"deleteAllMessageReactions": (*handler).deleteAllMessageReactions,
	})
	registerErrorMarkers("REACTION_INVALID", "REACTIONS_TOO_MANY", "PARTICIPANT_ID_INVALID")
}

func (h *handler) extendedValues(w http.ResponseWriter, r *http.Request) (MessageExtendedGateway, map[string]string, bool) {
	g, ok := h.gateway.(MessageExtendedGateway)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "METHOD_NOT_FOUND")
		return nil, nil, false
	}
	values, err := requestValues(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST")
		return nil, nil, false
	}
	return g, values, true
}

func writeExtendedResult(w http.ResponseWriter, result bool, err error) {
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorDescription(err))
		return
	}
	writeAPIOK(w, result)
}

func (h *handler) setMessageReaction(w http.ResponseWriter, r *http.Request, botID int64) {
	g, v, ok := h.extendedValues(w, r)
	if !ok {
		return
	}
	target, desc := h.parseEditTarget(r.Context(), botID, v)
	if desc != "" || target.inline {
		if desc == "" {
			desc = "MESSAGE_IDENTIFIER_INVALID"
		}
		writeAPIError(w, 400, desc)
		return
	}
	var raw []struct {
		Type   string `json:"type"`
		Emoji  string `json:"emoji"`
		Custom string `json:"custom_emoji_id"`
	}
	if v["reaction"] != "" && json.Unmarshal([]byte(v["reaction"]), &raw) != nil {
		writeAPIError(w, 400, "REACTION_INVALID")
		return
	}
	if len(raw) > 1 {
		writeAPIError(w, 400, "REACTIONS_TOO_MANY")
		return
	}
	reactions := make([]domain.MessageReaction, 0, len(raw))
	for _, item := range raw {
		switch item.Type {
		case "emoji":
			reactions = append(reactions, domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: item.Emoji})
		case "custom_emoji":
			id, err := strconv.ParseInt(item.Custom, 10, 64)
			if err != nil || id <= 0 {
				writeAPIError(w, 400, "REACTION_INVALID")
				return
			}
			reactions = append(reactions, domain.MessageReaction{Type: domain.MessageReactionCustomEmoji, DocumentID: id})
		default:
			writeAPIError(w, 400, "REACTION_INVALID")
			return
		}
	}
	result, err := g.BotAPISetMessageReaction(r.Context(), botID, target.chatID, target.messageID, reactions, apiBool(v["is_big"]))
	writeExtendedResult(w, result, err)
}

func (h *handler) deleteMessageReaction(w http.ResponseWriter, r *http.Request, botID int64) {
	h.deleteReactions(w, r, botID, false)
}

func (h *handler) deleteAllMessageReactions(w http.ResponseWriter, r *http.Request, botID int64) {
	h.deleteReactions(w, r, botID, true)
}

func (h *handler) deleteReactions(w http.ResponseWriter, r *http.Request, botID int64, all bool) {
	g, v, ok := h.extendedValues(w, r)
	if !ok {
		return
	}
	chat, err := h.chatIDParam(r.Context(), botID, v["chat_id"])
	actor := domain.Peer{}
	if err == nil {
		if (v["user_id"] == "") == (v["actor_chat_id"] == "") {
			err = errors.New("PARTICIPANT_ID_INVALID")
		} else if v["user_id"] != "" {
			actor.ID, err = strconv.ParseInt(v["user_id"], 10, 64)
			actor.Type = domain.PeerTypeUser
			if actor.ID <= 0 {
				err = errors.New("PARTICIPANT_ID_INVALID")
			}
		} else {
			var id int64
			id, err = h.chatIDParam(r.Context(), botID, v["actor_chat_id"])
			if id >= 0 {
				err = errors.New("PARTICIPANT_ID_INVALID")
			} else {
				actor = domain.Peer{Type: domain.PeerTypeChannel, ID: -id}
				if -id > 1000000000000 {
					actor.ID = -id - 1000000000000
				}
			}
		}
	}
	var result bool
	if err == nil {
		if all {
			result, err = g.BotAPIDeleteAllMessageReactions(r.Context(), botID, chat, actor)
		} else {
			result, err = g.BotAPIDeleteMessageReaction(r.Context(), botID, chat, apiInt(v["message_id"], 0), actor)
		}
	}
	writeExtendedResult(w, result, err)
}
