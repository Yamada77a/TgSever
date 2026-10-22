package botapi

import (
	"context"
	"net/http"
	"strconv"
	"unicode/utf8"

	"telesrv/internal/domain"
)

type SuggestedPostGateway interface {
	BotAPIToggleSuggestedPost(ctx context.Context, botID, chatID int64, messageID int, reject bool, comment string, sendDate int) error
}

func init() {
	registerMethods(map[string]methodFunc{
		"approveSuggestedPost": (*handler).approveSuggestedPost,
		"declineSuggestedPost": (*handler).declineSuggestedPost,
	})
	registerErrorMarkers("SUGGESTED_POST_ALREADY_HANDLED", "SUGGESTED_POST_INVALID", "SUGGESTED_POST_BALANCE_TOO_LOW", "COMMENT_INVALID", "SEND_DATE_INVALID")
	registerMessageProjection(projectSuggestedPost)
}

func (h *handler) approveSuggestedPost(w http.ResponseWriter, r *http.Request, botID int64) {
	h.toggleSuggestedPost(w, r, botID, false)
}
func (h *handler) declineSuggestedPost(w http.ResponseWriter, r *http.Request, botID int64) {
	h.toggleSuggestedPost(w, r, botID, true)
}

func (h *handler) toggleSuggestedPost(w http.ResponseWriter, r *http.Request, botID int64, reject bool) {
	gateway, ok := h.gateway.(SuggestedPostGateway)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "METHOD_NOT_FOUND")
		return
	}
	values, err := requestValues(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	chatID, err := strconv.ParseInt(values["chat_id"], 10, 64)
	if err != nil || chatID == 0 {
		writeAPIError(w, http.StatusBadRequest, "CHAT_ID_INVALID")
		return
	}
	messageID, err := strconv.Atoi(values["message_id"])
	if err != nil || messageID <= 0 || messageID > domain.MaxMessageBoxID {
		writeAPIError(w, http.StatusBadRequest, "MESSAGE_ID_INVALID")
		return
	}
	sendDate := 0
	if !reject && values["send_date"] != "" {
		sendDate, err = strconv.Atoi(values["send_date"])
		if err != nil || sendDate <= 0 {
			writeAPIError(w, http.StatusBadRequest, "SEND_DATE_INVALID")
			return
		}
	}
	comment := ""
	if reject {
		comment = values["comment"]
	}
	if utf8.RuneCountInString(comment) > 128 {
		writeAPIError(w, http.StatusBadRequest, "COMMENT_INVALID")
		return
	}
	if err := gateway.BotAPIToggleSuggestedPost(r.Context(), botID, chatID, messageID, reject, comment, sendDate); err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorDescription(err))
		return
	}
	writeAPIOK(w, true)
}

func suggestedPostPrice(price *domain.SuggestedPostPrice) map[string]any {
	if price == nil {
		return nil
	}
	currency := "XTR"
	if price.Kind == domain.SuggestedPostPriceTON {
		currency = "TON"
	}
	return map[string]any{"currency": currency, "amount": price.Amount}
}

func projectSuggestedPost(msg domain.Message, out map[string]any) {
	if post := msg.SuggestedPost; post != nil {
		state := "pending"
		if post.Accepted {
			state = "approved"
		} else if post.Rejected {
			state = "declined"
		}
		info := map[string]any{"state": state}
		if price := suggestedPostPrice(post.Price); price != nil {
			info["price"] = price
		}
		if post.ScheduleDate > 0 {
			info["send_date"] = post.ScheduleDate
		}
		out["suggested_post_info"] = info
	}
	if msg.SavedPeer.Type == domain.PeerTypeUser && msg.SavedPeer.ID > 0 {
		out["direct_messages_topic"] = map[string]any{"topic_id": msg.SavedPeer.ID}
	}
	action := msg.Action
	if action == nil {
		return
	}
	service := map[string]any{}
	switch action.Type {
	case domain.ChannelActionSuggestedPostApproval:
		if action.SuggestedPostRejected {
			if action.SuggestedPostRejectComment != "" {
				service["comment"] = action.SuggestedPostRejectComment
			}
			out["suggested_post_declined"] = service
		} else if action.SuggestedPostBalanceTooLow {
			service["price"] = suggestedPostPrice(action.SuggestedPostPrice)
			out["suggested_post_approval_failed"] = service
		} else {
			service["send_date"] = action.SuggestedPostScheduleDate
			if price := suggestedPostPrice(action.SuggestedPostPrice); price != nil {
				service["price"] = price
			}
			out["suggested_post_approved"] = service
		}
	case domain.ChannelActionSuggestedPostSuccess:
		price := action.SuggestedPostPrice
		if price == nil {
			return
		}
		if price.Kind == domain.SuggestedPostPriceTON {
			service["currency"], service["amount"] = "TON", price.Amount
		} else {
			service["currency"] = "XTR"
			service["star_amount"] = map[string]any{"amount": price.Amount, "nanostar_amount": price.Nanos}
		}
		out["suggested_post_paid"] = service
	case domain.ChannelActionSuggestedPostRefund:
		reason := "post_deleted"
		if action.SuggestedPostPayerInitiated {
			reason = "payment_refunded"
		}
		service["reason"] = reason
		out["suggested_post_refunded"] = service
	}
}
