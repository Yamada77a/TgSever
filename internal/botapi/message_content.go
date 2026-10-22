package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"telesrv/internal/domain"
)

type LiveLocationEdit = struct {
	Stop                                          bool
	Latitude, Longitude, Accuracy                 float64
	Period, Heading, Radius                       int
	SetAccuracy, SetPeriod, SetHeading, SetRadius bool
}

type ChecklistSendOptions = struct {
	Silent, Protect bool
	EffectID        int64
	ReplyID         int
	ReplyParameters *domain.BotAPIReplyParameters
}

type MessageContentGateway interface {
	BotAPIDeleteMessages(context.Context, int64, int64, []int) (bool, error)
	BotAPIEditLiveLocation(context.Context, int64, int64, int, string, LiveLocationEdit, bool, *domain.MessageReplyMarkup) (domain.Message, error)
	BotAPIEditInlineLiveLocation(context.Context, int64, domain.BotInlineMessageID, LiveLocationEdit, bool, *domain.MessageReplyMarkup) (bool, error)
	BotAPISendChecklist(context.Context, int64, int64, string, domain.MessageTodo, ChecklistSendOptions, *domain.MessageReplyMarkup) (domain.Message, error)
	BotAPIEditChecklist(context.Context, int64, int64, int, string, domain.MessageTodo, bool, *domain.MessageReplyMarkup) (domain.Message, error)
}

func init() {
	registerMethods(map[string]methodFunc{
		"deleteMessages":          (*handler).deleteMessages,
		"editMessageLiveLocation": (*handler).editMessageLiveLocation,
		"stopMessageLiveLocation": (*handler).stopMessageLiveLocation,
		"sendChecklist":           (*handler).sendChecklist,
		"editMessageChecklist":    (*handler).editMessageChecklist,
	})
	registerErrorMarkers("CHECKLIST_INVALID", "BUSINESS_CONNECTION_INVALID", "LOCATION_INVALID", "MESSAGE_IDS_INVALID", "ENTITY_TYPE_INVALID")
}

func (h *handler) contentValues(w http.ResponseWriter, r *http.Request) (MessageContentGateway, map[string]string, bool) {
	g, ok := h.gateway.(MessageContentGateway)
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

func (h *handler) deleteMessages(w http.ResponseWriter, r *http.Request, botID int64) {
	g, v, ok := h.contentValues(w, r)
	if !ok {
		return
	}
	chat, err := h.chatIDParam(r.Context(), botID, v["chat_id"])
	var ids []int
	if err == nil {
		if json.Unmarshal([]byte(v["message_ids"]), &ids) != nil || len(ids) < 1 || len(ids) > 100 {
			err = errors.New("MESSAGE_IDS_INVALID")
		}
	}
	if err == nil {
		for _, id := range ids {
			if id <= 0 || id > domain.MaxMessageBoxID {
				err = errors.New("MESSAGE_ID_INVALID")
				break
			}
		}
	}
	var result bool
	if err == nil {
		result, err = g.BotAPIDeleteMessages(r.Context(), botID, chat, ids)
	}
	writeExtendedResult(w, result, err)
}

func parseLiveEdit(v map[string]string, stop bool) (LiveLocationEdit, error) {
	out := LiveLocationEdit{Stop: stop}
	if stop {
		return out, nil
	}
	var err error
	out.Latitude, err = strconv.ParseFloat(v["latitude"], 64)
	if err != nil {
		return out, errors.New("LOCATION_INVALID")
	}
	out.Longitude, err = strconv.ParseFloat(v["longitude"], 64)
	if err != nil || math.IsNaN(out.Latitude) || math.IsNaN(out.Longitude) || math.IsInf(out.Latitude, 0) || math.IsInf(out.Longitude, 0) || out.Latitude < -90 || out.Latitude > 90 || out.Longitude < -180 || out.Longitude > 180 {
		return out, errors.New("LOCATION_INVALID")
	}
	if raw, ok := v["horizontal_accuracy"]; ok {
		out.SetAccuracy = true
		out.Accuracy, err = strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(out.Accuracy) || math.IsInf(out.Accuracy, 0) || out.Accuracy < 0 || out.Accuracy > 1500 {
			return out, errors.New("LOCATION_INVALID")
		}
	}
	for _, field := range []struct {
		key      string
		value    *int
		set      *bool
		min, max int
	}{{"live_period", &out.Period, &out.SetPeriod, 60, math.MaxInt32}, {"heading", &out.Heading, &out.SetHeading, 1, 360}, {"proximity_alert_radius", &out.Radius, &out.SetRadius, 1, 100000}} {
		if raw, ok := v[field.key]; ok {
			*field.set = true
			*field.value, err = strconv.Atoi(raw)
			if err != nil || *field.value < field.min || *field.value > field.max {
				return out, errors.New("LOCATION_INVALID")
			}
		}
	}
	return out, nil
}
func (h *handler) editMessageLiveLocation(w http.ResponseWriter, r *http.Request, botID int64) {
	h.editLiveLocation(w, r, botID, false)
}
func (h *handler) stopMessageLiveLocation(w http.ResponseWriter, r *http.Request, botID int64) {
	h.editLiveLocation(w, r, botID, true)
}
func (h *handler) editLiveLocation(w http.ResponseWriter, r *http.Request, botID int64, stop bool) {
	g, v, ok := h.contentValues(w, r)
	if !ok {
		return
	}
	target, desc := h.parseEditTarget(r.Context(), botID, v)
	if desc != "" {
		writeAPIError(w, 400, desc)
		return
	}
	input, err := parseLiveEdit(v, stop)
	markup, set, markupErr := optionalInlineReplyMarkup(v)
	if err == nil {
		err = markupErr
	}
	var msg domain.Message
	var result bool
	if err == nil {
		if target.inline {
			if v["business_connection_id"] != "" {
				err = errors.New("MESSAGE_IDENTIFIER_INVALID")
			} else {
				result, err = g.BotAPIEditInlineLiveLocation(r.Context(), botID, target.inlineID, input, set, markup)
			}
		} else {
			msg, err = g.BotAPIEditLiveLocation(r.Context(), botID, target.chatID, target.messageID, v["business_connection_id"], input, set, markup)
		}
	}
	h.writeEditResult(w, r, botID, target, msg, result, err)
}

func checklistFromAPI(raw string) (domain.MessageTodo, error) {
	var input struct {
		Title     string          `json:"title"`
		ParseMode string          `json:"parse_mode"`
		Entities  json.RawMessage `json:"title_entities"`
		Tasks     []struct {
			ID        int             `json:"id"`
			Text      string          `json:"text"`
			ParseMode string          `json:"parse_mode"`
			Entities  json.RawMessage `json:"text_entities"`
		} `json:"tasks"`
		Append   bool `json:"others_can_add_tasks"`
		Complete bool `json:"others_can_mark_tasks_as_done"`
	}
	out := domain.MessageTodo{}
	if json.Unmarshal([]byte(raw), &input) != nil || len(input.Tasks) < 1 || len(input.Tasks) > 30 {
		return out, errors.New("CHECKLIST_INVALID")
	}
	title, entities, err := botAPIFormattedTextRaw(input.Title, input.ParseMode, string(input.Entities), 255, true)
	if err != nil {
		return out, err
	}
	out.Title, out.TitleEntities, out.OthersCanAppend, out.OthersCanComplete = title, entities, input.Append, input.Complete
	seen := make(map[int]bool, len(input.Tasks))
	for _, task := range input.Tasks {
		if task.ID <= 0 || seen[task.ID] {
			return out, errors.New("CHECKLIST_INVALID")
		}
		seen[task.ID] = true
		text, entities, err := botAPIFormattedTextRaw(task.Text, task.ParseMode, string(task.Entities), 100, true)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, domain.MessageTodoItem{ID: task.ID, Title: text, Entities: entities})
	}
	for _, entities := range append([][]domain.MessageEntity{out.TitleEntities}, checklistItemEntities(out.Items)...) {
		for _, entity := range entities {
			switch string(entity.Type) {
			case "bold", "italic", "underline", "strike", "spoiler", "custom_emoji", "formatted_date":
			default:
				return out, errors.New("ENTITY_TYPE_INVALID")
			}
		}
	}
	return out, nil
}
func checklistItemEntities(items []domain.MessageTodoItem) [][]domain.MessageEntity {
	out := make([][]domain.MessageEntity, 0, len(items))
	for _, item := range items {
		out = append(out, item.Entities)
	}
	return out
}
func (h *handler) sendChecklist(w http.ResponseWriter, r *http.Request, botID int64) {
	h.checklist(w, r, botID, false)
}
func (h *handler) editMessageChecklist(w http.ResponseWriter, r *http.Request, botID int64) {
	h.checklist(w, r, botID, true)
}
func (h *handler) checklist(w http.ResponseWriter, r *http.Request, botID int64, edit bool) {
	g, v, ok := h.contentValues(w, r)
	if !ok {
		return
	}
	chat, err := h.chatIDParam(r.Context(), botID, v["chat_id"])
	if err == nil && strings.TrimSpace(v["business_connection_id"]) == "" {
		err = errors.New("BUSINESS_CONNECTION_INVALID")
	}
	todo, todoErr := checklistFromAPI(v["checklist"])
	if err == nil {
		err = todoErr
	}
	markup, set, markupErr := optionalInlineReplyMarkup(v)
	if err == nil {
		err = markupErr
	}
	var msg domain.Message
	if err == nil {
		if edit {
			msg, err = g.BotAPIEditChecklist(r.Context(), botID, chat, apiInt(v["message_id"], 0), v["business_connection_id"], todo, set, markup)
		} else {
			var options domain.BotAPIMessageOptions
			options, err = botAPIMessageOptions(v)
			if err == nil {
				msg, err = g.BotAPISendChecklist(r.Context(), botID, chat, v["business_connection_id"], todo, ChecklistSendOptions{Silent: apiBool(v["disable_notification"]), Protect: options.ProtectContent, EffectID: options.EffectID, ReplyParameters: options.ReplyParameters}, markup)
			}
		}
	}
	if err != nil {
		writeAPIError(w, 400, apiErrorDescription(err))
		return
	}
	h.writeBotMessages(w, r, botID, []domain.Message{msg}, false)
}
