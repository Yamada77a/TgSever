package rpc

import (
	"context"
	"errors"
	"unicode/utf8"

	"telesrv/internal/domain"
)

type verificationChannelReader interface {
	GetChannelByID(context.Context, int64) (domain.Channel, error)
}

// Verification is an organization-owned mark, never the platform's verified flag.
func (r *Router) BotAPISetVerification(ctx context.Context, botID, chatID int64, enabled bool, description string) error {
	if _, err := r.callerBotID(WithUserID(ctx, botID)); err != nil {
		return err
	}
	if r.deps.BotVerifications == nil {
		return botVerifierForbiddenErr()
	}
	peer, ok := botAPIPeerFromChatID(chatID)
	if !ok {
		return peerIDInvalidErr()
	}
	if utf8.RuneCountInString(description) > 70 {
		return errors.New("CUSTOM_DESCRIPTION_INVALID")
	}
	if peer.Type == domain.PeerTypeChannel {
		reader, ok := r.deps.Channels.(verificationChannelReader)
		if !ok {
			return peerIDInvalidErr()
		}
		channel, err := reader.GetChannelByID(ctx, peer.ID)
		if err != nil || channel.Monoforum {
			return peerIDInvalidErr()
		}
	}
	if !enabled {
		mark, err := r.deps.BotVerifications.PeerVerification(ctx, peer)
		if err == nil && mark.VerifierBotID != botID {
			return botVerifierForbiddenErr()
		}
		if err != nil && !errors.Is(err, domain.ErrCustomVerificationNotFound) {
			return setCustomVerificationErr(err)
		}
	}
	_, err := r.deps.BotVerifications.SetCustomVerification(ctx, domain.SetCustomVerificationRequest{
		VerifierBotID: botID, CallerUserID: botID, Peer: peer, Enabled: enabled, CustomDescription: description,
	})
	if err != nil {
		return setCustomVerificationErr(err)
	}
	return nil
}

func (r *Router) BotAPIResolveVerificationChatUsername(ctx context.Context, botID int64, username string) (int64, error) {
	if _, err := r.callerBotID(WithUserID(ctx, botID)); err != nil {
		return 0, err
	}
	if users, ok := r.deps.Users.(UserIdentityService); ok {
		user, found, err := users.ResolveUsername(ctx, botID, username)
		if err != nil {
			return 0, usernameErr(err)
		}
		if found {
			if !user.Bot {
				return 0, peerIDInvalidErr()
			}
			return user.ID, nil
		}
	}
	return r.BotAPIResolveChatUsername(ctx, botID, username)
}
