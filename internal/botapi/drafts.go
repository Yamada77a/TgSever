package botapi

import (
	"context"
	"net/http"
	"strconv"

	"telesrv/internal/domain"
)

type MessageDraft = struct {
	ID                  int64
	ThreadID            int
	Text                string
	Entities            []domain.MessageEntity
	Rich                *domain.BotAPIRichMessageInput
	CanStop, KeepOnStop bool
}

type MessageDraftGateway interface {
	BotAPISendDraft(context.Context, int64, int64, MessageDraft) (bool, error)
}

func init() {
	registerMethods(map[string]methodFunc{
		"sendMessageDraft":     (*handler).sendMessageDraft,
		"sendRichMessageDraft": (*handler).sendRichMessageDraft,
	})
	registerErrorMarkers("DRAFT_ID_INVALID", "MESSAGE_THREAD_INVALID", "MESSAGE_GENERATION_STOPPED", "DRAFT_STOP_FORBIDDEN")
}

func (h *handler) draftValues(w http.ResponseWriter, r *http.Request) (MessageDraftGateway, map[string]string, bool) {
	g, ok := h.gateway.(MessageDraftGateway)
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

func (h *handler) sendMessageDraft(w http.ResponseWriter, r *http.Request, botID int64) {
	h.sendDraft(w, r, botID, false)
}
func (h *handler) sendRichMessageDraft(w http.ResponseWriter, r *http.Request, botID int64) {
	h.sendDraft(w, r, botID, true)
}
func (h *handler) sendDraft(w http.ResponseWriter, r *http.Request, botID int64, rich bool) {
	g, v, ok := h.draftValues(w, r)
	if !ok {
		return
	}
	chat, err := strconv.ParseInt(v["chat_id"], 10, 64)
	if err != nil || chat <= 0 {
		writeAPIError(w, 400, "CHAT_ID_INVALID")
		return
	}
	draft := MessageDraft{CanStop: apiBool(v["can_stop"]), KeepOnStop: apiBool(v["keep_on_stop"])}
	draft.ID, err = strconv.ParseInt(v["draft_id"], 10, 64)
	if err != nil || draft.ID == 0 {
		writeAPIError(w, 400, "DRAFT_ID_INVALID")
		return
	}
	if raw, ok := v["message_thread_id"]; ok {
		draft.ThreadID, err = strconv.Atoi(raw)
		if err != nil || draft.ThreadID <= 0 || draft.ThreadID > domain.MaxMessageBoxID {
			writeAPIError(w, 400, "MESSAGE_THREAD_INVALID")
			return
		}
	}
	if rich {
		var input domain.BotAPIRichMessageInput
		input, err = richMessageInputFromAPI(v["rich_message"])
		draft.Rich = &input
	} else {
		draft.Text, draft.Entities, err = botAPIFormattedTextRaw(v["text"], v["parse_mode"], v["entities"], 4096, false)
	}
	var result bool
	if err == nil {
		result, err = g.BotAPISendDraft(r.Context(), botID, chat, draft)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorDescription(err))
		return
	}
	writeAPIOK(w, result)
}
