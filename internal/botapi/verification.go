package botapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

type VerificationGateway interface {
	BotAPISetVerification(ctx context.Context, botID, chatID int64, enabled bool, description string) error
	BotAPIResolveVerificationChatUsername(ctx context.Context, botID int64, username string) (int64, error)
}

func init() {
	registerMethods(map[string]methodFunc{
		"verifyUser":             (*handler).verifyUser,
		"verifyChat":             (*handler).verifyChat,
		"removeUserVerification": (*handler).removeUserVerification,
		"removeChatVerification": (*handler).removeChatVerification,
	})
	registerErrorMarkers("BOT_VERIFIER_FORBIDDEN", "CUSTOM_DESCRIPTION_INVALID")
}

func (h *handler) verifyUser(w http.ResponseWriter, r *http.Request, botID int64) {
	h.setVerification(w, r, botID, true, true)
}
func (h *handler) verifyChat(w http.ResponseWriter, r *http.Request, botID int64) {
	h.setVerification(w, r, botID, false, true)
}
func (h *handler) removeUserVerification(w http.ResponseWriter, r *http.Request, botID int64) {
	h.setVerification(w, r, botID, true, false)
}
func (h *handler) removeChatVerification(w http.ResponseWriter, r *http.Request, botID int64) {
	h.setVerification(w, r, botID, false, false)
}

func (h *handler) setVerification(w http.ResponseWriter, r *http.Request, botID int64, userOnly, enabled bool) {
	gateway, ok := h.gateway.(VerificationGateway)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "METHOD_NOT_FOUND")
		return
	}
	values, err := requestValues(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	var id int64
	if userOnly {
		id, err = strconv.ParseInt(values["user_id"], 10, 64)
		if err != nil || id <= 0 {
			writeAPIError(w, http.StatusBadRequest, "USER_ID_INVALID")
			return
		}
	} else {
		raw := strings.TrimSpace(values["chat_id"])
		if username, found := strings.CutPrefix(raw, "@"); found {
			if !validChatUsername(username) {
				err = errors.New("CHAT_ID_INVALID")
			} else {
				id, err = gateway.BotAPIResolveVerificationChatUsername(r.Context(), botID, username)
			}
		} else {
			id, err = strconv.ParseInt(raw, 10, 64)
		}
		if err != nil || id == 0 {
			writeAPIError(w, http.StatusBadRequest, "CHAT_ID_INVALID")
			return
		}
	}
	description := values["custom_description"]
	if !enabled {
		description = ""
	}
	if utf8.RuneCountInString(description) > 70 {
		writeAPIError(w, http.StatusBadRequest, "CUSTOM_DESCRIPTION_INVALID")
		return
	}
	if err := gateway.BotAPISetVerification(r.Context(), botID, id, enabled, description); err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorDescription(err))
		return
	}
	writeAPIOK(w, true)
}
