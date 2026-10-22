package botapi

import (
	"strconv"

	"telesrv/internal/domain"
)

// ParseOwnedGiftFilter is shared with getBusinessAccountGifts. Filtering belongs
// in the store before pagination, never in the JSON projection.
func ParseOwnedGiftFilter(values map[string]string) (domain.SavedStarGiftFilter, bool) {
	filter := domain.SavedStarGiftFilter{BotAPI: true, Offset: values["offset"], Limit: 100}
	if raw := values["limit"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return filter, false
		}
		filter.Limit = n
	}
	fields := [...]struct {
		name   string
		target *bool
	}{
		{"exclude_unsaved", &filter.ExcludeUnsaved}, {"exclude_saved", &filter.ExcludeSaved},
		{"exclude_unlimited", &filter.ExcludeUnlimited}, {"exclude_unique", &filter.ExcludeUnique},
		{"exclude_limited_upgradable", &filter.ExcludeLimitedUpgradable},
		{"exclude_limited_non_upgradable", &filter.ExcludeLimitedNonUpgradable},
		{"exclude_from_blockchain", &filter.ExcludeFromBlockchain}, {"sort_by_price", &filter.SortByValue},
	}
	for _, field := range fields {
		if raw := values[field.name]; raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return filter, false
			}
			*field.target = value
		}
	}
	return filter, true
}

func APIOwnedGifts(page domain.BotAPIOwnedGiftPage) map[string]any {
	gifts := make([]map[string]any, 0, len(page.Gifts))
	for _, item := range page.Gifts {
		gifts = append(gifts, apiOwnedGift(item))
	}
	out := map[string]any{"total_count": page.Count, "gifts": gifts}
	if page.NextOffset != "" {
		out["next_offset"] = page.NextOffset
	}
	return out
}

func apiOwnedGift(item domain.BotAPIOwnedGift) map[string]any {
	saved := item.Saved
	out := map[string]any{"send_date": saved.Date}
	if item.Sender != nil {
		out["sender_user"] = apiUser(*item.Sender)
	}
	if item.Business {
		if saved.MsgID > 0 {
			out["owned_gift_id"] = strconv.Itoa(saved.MsgID)
		}
		if !saved.Unsaved {
			out["is_saved"] = true
		}
	}
	if saved.Unique != nil {
		out["type"] = "unique"
		out["gift"] = apiOwnedUniqueGift(*saved.Unique, item.Publisher)
		if saved.CanTransferAt > 0 {
			out["next_transfer_date"] = saved.CanTransferAt
		}
		if item.Business && item.CanTransfer && saved.LifecycleStatus.Live() && saved.Unique.OwnerAddress == "" && !saved.Unique.Burned && saved.Unique.ResellAmount == nil {
			out["transfer_star_count"] = saved.TransferStars
			if saved.CanTransferAt <= item.Now {
				out["can_be_transferred"] = true
			}
		}
		return out
	}
	out["type"], out["gift"] = "regular", apiStarGift(item.Gift)
	if item.Publisher != nil {
		out["gift"].(map[string]any)["publisher_chat"] = apiGiftPublisher(*item.Publisher)
	}
	if saved.NameHidden {
		out["is_private"] = true
	}
	if saved.Message != "" {
		out["text"] = saved.Message
	}
	if entities := apiMessageEntities(saved.MessageEntities, item.EntityUsers); len(entities) > 0 {
		out["entities"] = entities
	}
	if saved.PrepaidUpgradeStars > 0 {
		out["prepaid_upgrade_star_count"] = saved.PrepaidUpgradeStars
	}
	if saved.GiftNum > 0 {
		out["unique_gift_number"] = saved.GiftNum
	}
	if item.Business {
		if item.Gift.UpgradeStars > 0 && item.Gift.UpgradeIssued < item.Gift.UpgradeTotal {
			out["can_be_upgraded"] = true
		}
		if saved.ConvertStars > 0 {
			out["convert_star_count"] = saved.ConvertStars
		}
		if saved.PrepaidUpgradeHash != "" && saved.PrepaidUpgradeStars > 0 {
			out["is_upgrade_separate"] = true
		}
	}
	return out
}

func apiGiftPublisher(c domain.Channel) map[string]any {
	kind := "supergroup"
	if c.Broadcast && !c.Megagroup {
		kind = "channel"
	}
	out := map[string]any{"id": botAPIChannelChatIDMin - c.ID, "type": kind, "title": c.Title}
	if c.Username != "" {
		out["username"] = c.Username
	}
	return out
}

func apiOwnedUniqueGift(g domain.UniqueStarGift, publisher *domain.Channel) map[string]any {
	attribute := func(a domain.StarGiftCollectibleAttribute) map[string]any {
		out := map[string]any{"name": a.Name, "rarity_per_mille": a.RarityPermille}
		if a.Document != nil {
			out["sticker"] = apiSticker(*a.Document)
		}
		return out
	}
	model := attribute(g.Model)
	if g.Model.Crafted {
		model["rarity_per_mille"] = 0
		if g.Model.RarityKind != domain.StarGiftRarityPermille {
			model["rarity"] = string(g.Model.RarityKind)
		}
	}
	backdrop := map[string]any{"name": g.Backdrop.Name, "rarity_per_mille": g.Backdrop.RarityPermille,
		"colors": map[string]any{"center_color": g.Backdrop.CenterColor, "edge_color": g.Backdrop.EdgeColor, "symbol_color": g.Backdrop.PatternColor, "text_color": g.Backdrop.TextColor}}
	out := map[string]any{"gift_id": strconv.FormatInt(g.GiftID, 10), "base_name": g.Title, "name": g.Slug, "number": g.Num, "model": model, "symbol": attribute(g.Pattern), "backdrop": backdrop}
	if g.RequirePremium {
		out["is_premium"] = true
	}
	if g.Burned {
		out["is_burned"] = true
	}
	if g.OwnerAddress != "" {
		out["is_from_blockchain"] = true
	}
	if publisher != nil {
		out["publisher_chat"] = apiGiftPublisher(*publisher)
	}
	return out
}
